package httpapi

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/765362546/novel-reader-go/internal/model"
)

// StartScheduler launches background jobs that mirror the original Kotlin
// @Scheduled tasks in YueduApi (shelfUpdateJob, clearUser).
// These are best-effort — they recover from panics and log errors but never
// crash the server. Call it from (*Server).Run before ListenAndServe.
func (s *Server) StartScheduler() {
	go s.shelfUpdateJob()
	go s.clearUserJob()
}

// shelfUpdateJob mirrors YueduApi.shelfUpdateJob: every 10 minutes, refresh
// chapter lists for every non-local, can-update book in every namespace so
// the latestChapterTitle / totalChapterNum stay up to date.
func (s *Server) shelfUpdateJob() {
	tick := time.NewTicker(10 * time.Minute)
	defer tick.Stop()
	for {
		<-tick.C
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("shelfUpdateJob panic: %v", r)
				}
			}()
			log.Println("[scheduler] shelfUpdateJob: start")
			s.refreshNamespace("default")
			if s.App.Secure {
				for _, u := range s.loadUsernames() {
					s.refreshNamespace(u)
				}
			}
			log.Println("[scheduler] shelfUpdateJob: done")
		}()
	}
}

func (s *Server) refreshNamespace(ns string) {
	books := s.loadBooks(ns)
	if len(books) == 0 {
		return
	}
	updated := false
	for _, book := range books {
		if book.Origin == model.BookTypeLocal {
			continue
		}
		if !book.CanUpdate {
			continue
		}
		srcStr := s.sourceStringFromRequest(nil, ns, book.Origin)
		if srcStr == "" {
			continue
		}
		var bs model.BookSource
		if json.Unmarshal([]byte(srcStr), &bs) != nil {
			continue
		}
		ch, err := s.chapterList(ns, book, srcStr, true)
		if err != nil || len(ch) == 0 {
			continue
		}
		book.TotalChapterNum = len(ch)
		if last := ch[len(ch)-1]; last != nil && last.Title != "" {
			t := last.Title
			book.LatestChapterTitle = &t
		}
		book.LastCheckTime = time.Now().UnixMilli()
		updated = true
	}
	if updated {
		if err := s.saveBooks(ns, books); err != nil {
			log.Printf("[scheduler] shelfUpdateJob save ns=%s failed: %v", ns, err)
		}
	}
}

// loadUsernames returns every user namespace when secure=true.
func (s *Server) loadUsernames() []string {
	b, err := s.Store.Get("data", "users")
	if err != nil {
		return nil
	}
	var users map[string]map[string]any
	if json.Unmarshal(b, &users) != nil {
		return nil
	}
	var out []string
	for _, u := range users {
		if ns, ok := u["username"].(string); ok && ns != "" {
			out = append(out, ns)
		}
	}
	return out
}

// clearUserJob mirrors YueduApi.clearUser: at 23:59 every day remove users
// whose last_login_at is older than autoClearInactiveUser days and wipe their
// data directory. Only runs when secure=true AND autoClearInactiveUser>0.
func (s *Server) clearUserJob() {
	// Wait until the next 23:59 local time, then fire daily.
	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 0, 0, time.Local)
	if !now.Before(next) {
		next = next.Add(24 * time.Hour)
	}
	first := time.NewTimer(next.Sub(now))
	defer first.Stop()
	<-first.C

	daily := time.NewTicker(24 * time.Hour)
	defer daily.Stop()

	for {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("clearUserJob panic: %v", r)
				}
			}()
			s.clearInactiveUsersOnce()
		}()
		<-daily.C
	}
}

func (s *Server) clearInactiveUsersOnce() {
	if !s.App.Secure || s.App.AutoClearInactiveUser <= 0 {
		return
	}
	b, err := s.Store.Get("data", "users")
	if err != nil {
		return
	}
	var users map[string]map[string]any
	if json.Unmarshal(b, &users) != nil {
		return
	}
	expireCutoff := time.Now().UnixMilli() - int64(s.App.AutoClearInactiveUser)*86400_000
	homeRoot := filepath.Join(s.Store.Root, "data")
	for key, u := range users {
		username, _ := u["username"].(string)
		if username == "" {
			continue
		}
		var lastLogin int64
		switch v := u["last_login_at"].(type) {
		case float64:
			lastLogin = int64(v)
		case json.Number:
			lastLogin, _ = v.Int64()
		}
		if lastLogin > 0 && lastLogin < expireCutoff {
			log.Printf("[scheduler] clearUserJob: deleting inactive user %s (last_login_at=%d)", username, lastLogin)
			userHome := filepath.Join(homeRoot, username)
			if st, err := os.Stat(userHome); err == nil && st.IsDir() {
				_ = os.RemoveAll(userHome)
			}
			delete(users, key)
		}
	}
	if err := s.Store.SavePretty(users, "data", "users"); err != nil {
		log.Printf("[scheduler] clearUserJob save users failed: %v", err)
	}
}
