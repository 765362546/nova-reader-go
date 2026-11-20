package httpapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"

	"github.com/765362546/novel-reader-go/internal/model"
)

// StartScheduler launches background jobs that mirror the original Kotlin
// @Scheduled tasks in YueduApi (shelfUpdateJob, clearUser).
// These are best-effort — they recover from panics and log errors but never
// crash the server. Call it from (*Server).Run before ListenAndServe.
func (s *Server) StartScheduler() {
	if s.refreshSem == nil {
		s.refreshSem = make(chan struct{}, refreshWorkers)
	}
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
					logger.Error("shelfUpdateJob panic", "panic", r, "stack", string(debug.Stack()))
				}
			}()
			logger.Info("shelf update job started")
			start := time.Now()
			s.Sessions.Cleanup()
			namespaces := []string{"default"}
			if s.App.Secure {
				namespaces = append(namespaces, s.loadUsernames()...)
			}
			// Namespaces refresh in parallel; refreshNamespace shares a single
			// server-wide semaphore so total outbound concurrency stays capped.
			var wg sync.WaitGroup
			for _, ns := range namespaces {
				ns := ns
				wg.Add(1)
				go func() {
					defer wg.Done()
					s.refreshNamespace(ns)
				}()
			}
			wg.Wait()
			logger.Info("shelf update job finished", "duration_ms", time.Since(start).Milliseconds())
		}()
	}
}

const (
	refreshWorkers     = 8
	refreshBookTimeout = 90 * time.Second
	// Books checked within this window are skipped by the periodic job.
	refreshSkipWindow = 5 * time.Minute
)

type refreshOutcome struct {
	index  int
	total  int
	latest string
	ok     bool
}

func (s *Server) refreshNamespace(ns string) {
	books := s.loadBooks(ns)
	if len(books) == 0 {
		return
	}
	sem := s.refreshSem
	if sem == nil {
		sem = make(chan struct{}, refreshWorkers)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	updated := false

	for i, book := range books {
		if book.Origin == model.BookTypeLocal || !book.CanUpdate {
			continue
		}
		if book.LastCheckTime > 0 && time.Since(time.UnixMilli(book.LastCheckTime)) < refreshSkipWindow {
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
		wg.Add(1)
		go func(index int, snapshot model.Book, srcStr string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			done := make(chan refreshOutcome, 1)
			go func() {
				done <- s.refreshBookChapters(ns, &snapshot, srcStr, index)
			}()
			select {
			case out := <-done:
				if !out.ok {
					return
				}
				books[index].TotalChapterNum = out.total
				if out.latest != "" {
					books[index].LatestChapterTitle = out.latest
				}
				books[index].LastCheckTime = time.Now().UnixMilli()
				mu.Lock()
				updated = true
				mu.Unlock()
			case <-time.After(refreshBookTimeout):
				logger.Warn("shelf update: refresh timed out",
					"namespace", ns, "book", snapshot.Name)
			}
		}(i, *book, srcStr)
	}
	wg.Wait()
	if updated {
		if err := s.saveBooks(ns, books); err != nil {
			logger.Error("shelf update: save books failed", "namespace", ns, "error", err)
		}
	}
}

// refreshBookChapters fetches a fresh chapter list for one book. It operates on
// a snapshot so a timed-out refresh can never race with the final shelf save.
func (s *Server) refreshBookChapters(ns string, book *model.Book, srcStr string, index int) refreshOutcome {
	ch, err := s.chapterList(ns, book, srcStr, true)
	if err != nil || len(ch) == 0 {
		logger.Warn("shelf update: refresh chapters failed",
			"namespace", ns, "book", book.Name, "error", err)
		return refreshOutcome{index: index}
	}
	out := refreshOutcome{index: index, total: len(ch), ok: true}
	if last := ch[len(ch)-1]; last != nil && last.Title != "" {
		out.latest = last.Title
	}
	return out
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
					logger.Error("clearUserJob panic", "panic", r, "stack", string(debug.Stack()))
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
			logger.Info("clear user job: removing inactive user",
				"username", username,
				"lastLoginAt", lastLogin,
			)
			userHome := filepath.Join(homeRoot, username)
			if st, err := os.Stat(userHome); err == nil && st.IsDir() {
				_ = os.RemoveAll(userHome)
			}
			delete(users, key)
		}
	}
	if err := s.Store.SavePretty(users, "data", "users"); err != nil {
		logger.Error("clear user job: save users failed", "error", err)
	}
}
