package service

import (
	"context"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"qiandao/internal/domain"
	"qiandao/internal/store"
)

type Scheduler struct {
	store   store.Store
	service *SignInService
	stop    chan struct{}
	wg      sync.WaitGroup
	mu      sync.Mutex
	lastRun map[string]string
}

func NewScheduler(st store.Store, svc *SignInService) *Scheduler {
	return &Scheduler{
		store:   st,
		service: svc,
		stop:    make(chan struct{}),
		lastRun: map[string]string{},
	}
}

func (s *Scheduler) Start() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.tick(time.Now())
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.tick(time.Now())
			case <-s.stop:
				return
			}
		}
	}()
}

func (s *Scheduler) Stop() {
	close(s.stop)
	s.wg.Wait()
}

func (s *Scheduler) tick(now time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return
	}
	loc, err := time.LoadLocation(settings.Timezone)
	if err != nil {
		loc = time.Local
	}
	now = now.In(loc)
	accounts, err := s.store.ListAccounts(ctx, domain.AccountFilter{OnlyEnabled: true})
	if err != nil {
		return
	}
	for _, account := range accounts {
		if !cronMatches(account.Cron, now) {
			continue
		}
		key := account.ID + ":" + now.Format("2006-01-02 15:04")
		if s.alreadyRun(key) {
			continue
		}
		ok, err := s.store.HasSuccessToday(ctx, account.ID, loc)
		if err == nil && ok {
			continue
		}
		delay := time.Duration(0)
		if settings.Scheduler.RandomDelaySeconds > 0 {
			delay = time.Duration(rand.Intn(settings.Scheduler.RandomDelaySeconds+1)) * time.Second
		}
		s.wg.Add(1)
		go func(acc domain.Account) {
			defer s.wg.Done()
			if delay > 0 {
				select {
				case <-time.After(delay):
				case <-s.stop:
					return
				}
			}
			runCtx, runCancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_, _ = s.service.signInLoadedAccount(runCtx, acc, "schedule")
			runCancel()
		}(account)
	}
}

func (s *Scheduler) alreadyRun(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.lastRun[key]; ok {
		return true
	}
	s.lastRun[key] = key
	if len(s.lastRun) > 5000 {
		s.lastRun = map[string]string{key: key}
	}
	return false
}

func cronMatches(expr string, now time.Time) bool {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return false
	}
	parts := strings.Fields(expr)
	if len(parts) < 2 {
		return false
	}
	minute, ok := cronPartMatches(parts[0], now.Minute())
	if !ok || !minute {
		return false
	}
	hour, ok := cronPartMatches(parts[1], now.Hour())
	return ok && hour
}

func cronPartMatches(part string, value int) (bool, bool) {
	part = strings.TrimSpace(part)
	if part == "*" {
		return true, true
	}
	for _, token := range strings.Split(part, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		v, err := strconv.Atoi(token)
		if err != nil {
			return false, false
		}
		if v == value {
			return true, true
		}
	}
	return false, true
}
