package store

import (
	"context"
	"errors"
	"time"

	"qiandao/internal/domain"
)

var ErrNotFound = errors.New("not found")

type Store interface {
	Close() error
	GetSettings(ctx context.Context) (domain.Settings, error)
	SaveSettings(ctx context.Context, settings domain.Settings) error
	ListAccounts(ctx context.Context, filter domain.AccountFilter) ([]domain.Account, error)
	GetAccount(ctx context.Context, id string) (domain.Account, error)
	SaveAccount(ctx context.Context, account domain.Account) error
	DeleteAccount(ctx context.Context, id string) error
	SaveRecord(ctx context.Context, result domain.SignInResult) error
	ListRecords(ctx context.Context, filter domain.RecordFilter) ([]domain.SignInResult, error)
	HasSuccessToday(ctx context.Context, accountID string, loc *time.Location) (bool, error)
}
