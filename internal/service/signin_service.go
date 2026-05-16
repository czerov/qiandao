package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"qiandao/internal/domain"
	"qiandao/internal/notifier"
	"qiandao/internal/provider"
	"qiandao/internal/store"
)

type SignInService struct {
	store     store.Store
	providers map[string]provider.Provider
	notifier  *notifier.Notifier
	locksMu   sync.Mutex
	locks     map[string]*sync.Mutex
}

func NewSignInService(st store.Store, providers []provider.Provider, nt *notifier.Notifier) *SignInService {
	m := make(map[string]provider.Provider, len(providers))
	for _, p := range providers {
		m[p.Platform()] = p
	}
	return &SignInService{
		store:     st,
		providers: m,
		notifier:  nt,
		locks:     map[string]*sync.Mutex{},
	}
}

func (s *SignInService) SignInAccount(ctx context.Context, accountID, trigger string) (domain.SignInResult, error) {
	account, err := s.store.GetAccount(ctx, accountID)
	if err != nil {
		return domain.SignInResult{}, err
	}
	return s.signInLoadedAccount(ctx, account, trigger)
}

func (s *SignInService) SignInAll(ctx context.Context, platform string, onlyEnabled bool, trigger string) ([]domain.SignInResult, error) {
	accounts, err := s.store.ListAccounts(ctx, domain.AccountFilter{Platform: strings.TrimSpace(platform), OnlyEnabled: onlyEnabled})
	if err != nil {
		return nil, err
	}
	results := make([]domain.SignInResult, 0, len(accounts))
	var errs []string
	for _, account := range accounts {
		result, err := s.signInLoadedAccount(ctx, account, trigger)
		results = append(results, result)
		if err != nil {
			errs = append(errs, account.DisplayName()+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return results, errors.New(strings.Join(errs, "; "))
	}
	return results, nil
}

func (s *SignInService) signInLoadedAccount(ctx context.Context, account domain.Account, trigger string) (domain.SignInResult, error) {
	if strings.TrimSpace(trigger) == "" {
		trigger = "manual"
	}
	lock := s.accountLock(account.ID)
	lock.Lock()
	defer lock.Unlock()

	started := time.Now()
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return domain.SignInResult{}, err
	}
	p, ok := s.providers[account.Platform]
	if !ok {
		return domain.SignInResult{}, fmt.Errorf("不支持的平台: %s", account.Platform)
	}
	pr, signErr := p.SignIn(ctx, account, settings)
	result := pr.SignInResult
	result.ID = domain.NewID("rec")
	result.AccountID = account.ID
	result.Platform = account.Platform
	result.AccountName = account.DisplayName()
	result.Trigger = trigger
	result.StartedAt = started
	result.FinishedAt = time.Now()
	if result.Message == "" && signErr != nil {
		result.Message = signErr.Error()
	}
	if signErr != nil {
		result.Success = false
	}
	if result.Message == "" {
		if result.Success {
			result.Message = "签到成功"
		} else {
			result.Message = "签到失败"
		}
	}
	if err := s.store.SaveRecord(ctx, result); err != nil {
		return result, err
	}
	if pr.CredentialPatch != nil {
		account.Credential = *pr.CredentialPatch
		account.UpdatedAt = time.Now()
		_ = s.store.SaveAccount(ctx, account)
	}
	if s.notifier != nil && (account.NotifyTG || account.NotifyWebhook) {
		notifyCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		_ = s.notifier.Send(notifyCtx, result, account, settings)
		cancel()
	}
	return result, signErr
}

func (s *SignInService) accountLock(accountID string) *sync.Mutex {
	s.locksMu.Lock()
	defer s.locksMu.Unlock()
	lock := s.locks[accountID]
	if lock == nil {
		lock = &sync.Mutex{}
		s.locks[accountID] = lock
	}
	return lock
}
