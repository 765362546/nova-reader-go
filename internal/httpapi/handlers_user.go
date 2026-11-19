package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/765362546/novel-reader-go/internal/model"
)

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9]+$`)

func (s *Server) login(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	body := bodyMap(r)
	username := str(body, "username")
	password := str(body, "password")
	isLogin, _ := boolVal(body, "isLogin")
	if username == "" {
		return failure("请输入用户名")
	}
	if password == "" {
		return failure("请输入密码")
	}
	users := s.loadUsers()
	existed, ok := users[username]
	if !ok {
		if isLogin {
			return failure("用户不存在")
		}
		if len(username) < 5 {
			return failure("用户名不能低于5位")
		}
		if len(password) < 8 {
			return failure("密码不能低于8位")
		}
		if username == "default" {
			return failure("用户名不能为非法字符")
		}
		if !usernamePattern.MatchString(username) {
			return failure("用户名只能由字母和数字组成")
		}
		if s.App.InviteCode != "" {
			code := str(body, "code")
			if code == "" {
				return failure("请输入邀请码")
			}
			if code != s.App.InviteCode {
				return failure("邀请码错误")
			}
		}
		if len(users) >= s.App.UserLimit {
			return failure("超过用户数上限")
		}
		salt := randomString(8)
		u := &model.User{
			Username:  username,
			Password:  genEncryptedPassword(password, salt),
			Salt:      salt,
			CreatedAt: time.Now().UnixMilli(),
			TokenMap:  map[string]int64{},
		}
		return success(s.saveUserSession(w, username, users, u, false))
	}
	if !isLogin {
		return failure("用户名已被占用")
	}
	if genEncryptedPassword(password, existed.Salt) != existed.Password {
		return failure("密码错误")
	}
	return success(s.saveUserSession(w, username, users, existed, false))
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	if !s.App.Secure {
		return failure("不支持的操作")
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.Sessions.destroy(c.Value)
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	}
	accessToken := r.URL.Query().Get("accessToken")
	if accessToken != "" {
		if username, token, ok := splitToken(accessToken); ok {
			users := s.loadUsers()
			if u := users[username]; u != nil {
				delete(u.TokenMap, token)
				if u.Token == token {
					u.Token = ""
				}
				_ = s.saveUsers(users)
			}
		}
	}
	return &ReturnData{IsSuccess: false, ErrorMsg: "请重新登录", Data: "NEED_LOGIN"}
}

func (s *Server) getUserInfo(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	var userInfo any
	if ri.Username != "" && ri.User != nil {
		userInfo = formatUser(ri.User)
	}
	return success(map[string]any{
		"userInfo":  userInfo,
		"secure":    s.App.Secure,
		"secureKey": s.App.SecureKey != "",
	})
}

func (s *Server) saveUserConfig(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	var content map[string]any
	if json.Unmarshal(bodyBytes(r), &content) != nil {
		return failure("参数错误")
	}
	content["@updateTime"] = time.Now().UnixMilli()
	_ = s.Store.Save(content, "data", ri.managerNameSpace(), "userConfig")
	return success("")
}

func (s *Server) getUserConfig(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	b, err := s.Store.Get("data", ri.managerNameSpace(), "userConfig")
	if err != nil {
		return failure("没有备份文件")
	}
	var content map[string]any
	if json.Unmarshal(b, &content) != nil {
		return failure("没有备份文件")
	}
	return success(content)
}

func (s *Server) userListData() []map[string]any {
	users := s.loadUsers()
	out := []map[string]any{}
	for _, u := range users {
		out = append(out, formatUser(u))
	}
	return out
}

func (s *Server) getUserList(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	if !s.App.Secure || s.App.SecureKey == "" {
		return failure("不支持的操作")
	}
	if !s.managerOK(r, ri) {
		return failureSecureKey()
	}
	return success(s.userListData())
}

func (s *Server) addUser(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	if !s.App.Secure || s.App.SecureKey == "" {
		return failure("不支持的操作")
	}
	body := bodyMap(r)
	username := str(body, "username")
	password := str(body, "password")
	if username == "" {
		return failure("请输入用户名")
	}
	if password == "" {
		return failure("请输入密码")
	}
	if len(username) < 5 {
		return failure("用户名不能低于5位")
	}
	if len(password) < 8 {
		return failure("密码不能低于8位")
	}
	if username == "default" {
		return failure("用户名不能为非法字符")
	}
	if !s.managerOK(r, ri) {
		return failureSecureKey()
	}
	if !usernamePattern.MatchString(username) {
		return failure("用户名只能由字母和数字组成")
	}
	users := s.loadUsers()
	if _, ok := users[username]; ok {
		return failure("用户已存在")
	}
	if len(users) >= s.App.UserLimit {
		return failure("超过用户数上限")
	}
	salt := randomString(8)
	users[username] = &model.User{
		Username:  username,
		Password:  genEncryptedPassword(password, salt),
		Salt:      salt,
		CreatedAt: time.Now().UnixMilli(),
		TokenMap:  map[string]int64{},
	}
	_ = s.saveUsers(users)
	return success(s.userListData())
}

func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	if !s.App.Secure || s.App.SecureKey == "" {
		return failure("不支持的操作")
	}
	body := bodyMap(r)
	username := str(body, "username")
	password := str(body, "password")
	if username == "" {
		return failure("请输入用户名")
	}
	if password == "" {
		return failure("请输入密码")
	}
	if len(password) < 8 {
		return failure("密码不能低于8位")
	}
	if username == "default" {
		return failure("用户不存在")
	}
	if !s.managerOK(r, ri) {
		return failureSecureKey()
	}
	users := s.loadUsers()
	u := users[username]
	if u == nil {
		return failure("用户不存在")
	}
	salt := randomString(8)
	u.Salt = salt
	u.Password = genEncryptedPassword(password, salt)
	_ = s.saveUsers(users)
	return success("")
}

func (s *Server) deleteUsers(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	if !s.App.Secure || s.App.SecureKey == "" {
		return failure("不支持的操作")
	}
	if !s.managerOK(r, ri) {
		return failureSecureKey()
	}
	var usernames []string
	_ = json.Unmarshal(bodyBytes(r), &usernames)
	users := s.loadUsers()
	for _, name := range usernames {
		if _, ok := users[name]; !ok {
			continue
		}
		delete(users, name)
		_ = os.RemoveAll(filepath.Join(s.Store.Root, "data", name))
	}
	_ = s.saveUsers(users)
	return success(s.userListData())
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	if !s.App.Secure || s.App.SecureKey == "" {
		return failure("不支持的操作")
	}
	if !s.managerOK(r, ri) {
		return failureSecureKey()
	}
	body := bodyMap(r)
	username := str(body, "username")
	if username == "" {
		return failure("参数错误")
	}
	users := s.loadUsers()
	u := users[username]
	if u == nil {
		return failure("用户不存在")
	}
	if v, ok := body["enableWebdav"].(bool); ok {
		u.EnableWebdav = v
	}
	if v, ok := body["enableLocalStore"].(bool); ok {
		u.EnableLocalStore = v
	}
	_ = s.saveUsers(users)
	return success(s.userListData())
}

func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	if err := r.ParseMultipartForm(128 << 20); err != nil {
		return failure("请上传文件")
	}
	typ := r.URL.Query().Get("type")
	if typ == "" {
		typ = "images"
	}
	ns := ri.managerNameSpace()
	var urls []string
	for _, fhs := range r.MultipartForm.File {
		for _, fh := range fhs {
			f, err := fh.Open()
			if err != nil {
				continue
			}
			b, _ := io.ReadAll(f)
			f.Close()
			name := filepath.Base(fh.Filename)
			dir := filepath.Join(s.Store.Root, "assets", ns, typ)
			_ = os.MkdirAll(dir, 0o755)
			if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
				continue
			}
			urls = append(urls, "/assets/"+ns+"/"+typ+"/"+name)
		}
	}
	if urls == nil {
		urls = []string{}
	}
	return success(urls)
}

func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request, ri *reqInfo) *ReturnData {
	if ri.NeedLogin {
		return failureLogin()
	}
	url := queryOrBody(r, bodyMap(r), "url")
	if url == "" {
		return failure("请输入文件链接")
	}
	ns := ri.managerNameSpace()
	prefix := "/assets/" + ns + "/"
	if !strings.HasPrefix(url, prefix) {
		return failure("文件链接错误")
	}
	_ = os.RemoveAll(filepath.Join(s.Store.Root, url))
	return success("")
}
