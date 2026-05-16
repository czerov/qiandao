package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"qiandao/internal/domain"

	_ "modernc.org/sqlite"
)

const settingsKey = "app"

type SQLiteStore struct {
	db *sql.DB
}

func OpenSQLite(path string) (*SQLiteStore, error) {
	if strings.TrimSpace(path) == "" {
		path = filepath.Join("data", "signin.db")
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	s := &SQLiteStore{db: db}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.ensureSettings(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) migrate(ctx context.Context) error {
	stmts := []string{
		`PRAGMA journal_mode=DELETE;`,
		`PRAGMA busy_timeout=5000;`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS accounts (
			id TEXT PRIMARY KEY,
			platform TEXT NOT NULL,
			name TEXT,
			enabled INTEGER NOT NULL DEFAULT 1,
			cron TEXT,
			notify_tg INTEGER NOT NULL DEFAULT 1,
			notify_webhook INTEGER NOT NULL DEFAULT 1,
			credential_json TEXT NOT NULL,
			options_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_accounts_platform ON accounts(platform);`,
		`CREATE INDEX IF NOT EXISTS idx_accounts_enabled ON accounts(enabled);`,
		`CREATE TABLE IF NOT EXISTS signin_records (
			id TEXT PRIMARY KEY,
			account_id TEXT NOT NULL,
			platform TEXT NOT NULL,
			account_name TEXT,
			trigger TEXT,
			success INTEGER NOT NULL,
			message TEXT,
			mode TEXT,
			username TEXT,
			nickname TEXT,
			email TEXT,
			signin_days INTEGER,
			reward_points INTEGER,
			total_points INTEGER,
			raw TEXT,
			started_at TEXT NOT NULL,
			finished_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_signin_records_account ON signin_records(account_id, finished_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_signin_records_platform ON signin_records(platform, finished_at DESC);`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) ensureSettings(ctx context.Context) error {
	_, err := s.GetSettings(ctx)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	return s.SaveSettings(ctx, domain.DefaultSettings())
}

func (s *SQLiteStore) GetSettings(ctx context.Context) (domain.Settings, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, settingsKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Settings{}, ErrNotFound
	}
	if err != nil {
		return domain.Settings{}, err
	}
	var settings domain.Settings
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return domain.Settings{}, err
	}
	return settings.WithDefaults(), nil
}

func (s *SQLiteStore) SaveSettings(ctx context.Context, settings domain.Settings) error {
	raw, err := json.Marshal(settings.WithDefaults())
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO settings(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, settingsKey, string(raw))
	return err
}

func (s *SQLiteStore) ListAccounts(ctx context.Context, filter domain.AccountFilter) ([]domain.Account, error) {
	query := `SELECT id, platform, name, enabled, cron, notify_tg, notify_webhook, credential_json, options_json, created_at, updated_at FROM accounts`
	var args []any
	var where []string
	if strings.TrimSpace(filter.Platform) != "" {
		where = append(where, "platform = ?")
		args = append(args, strings.TrimSpace(filter.Platform))
	}
	if filter.OnlyEnabled {
		where = append(where, "enabled = 1")
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY platform ASC, name ASC, created_at ASC"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var accounts []domain.Account
	for rows.Next() {
		acc, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, acc)
	}
	return accounts, rows.Err()
}

func (s *SQLiteStore) GetAccount(ctx context.Context, id string) (domain.Account, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, platform, name, enabled, cron, notify_tg, notify_webhook, credential_json, options_json, created_at, updated_at
		FROM accounts WHERE id = ?
	`, id)
	acc, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Account{}, ErrNotFound
	}
	return acc, err
}

func (s *SQLiteStore) SaveAccount(ctx context.Context, account domain.Account) error {
	cred, err := json.Marshal(account.Credential)
	if err != nil {
		return err
	}
	opts, err := json.Marshal(account.Options)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO accounts(id, platform, name, enabled, cron, notify_tg, notify_webhook, credential_json, options_json, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			platform = excluded.platform,
			name = excluded.name,
			enabled = excluded.enabled,
			cron = excluded.cron,
			notify_tg = excluded.notify_tg,
			notify_webhook = excluded.notify_webhook,
			credential_json = excluded.credential_json,
			options_json = excluded.options_json,
			updated_at = excluded.updated_at
	`, account.ID, account.Platform, account.Name, boolInt(account.Enabled), account.Cron, boolInt(account.NotifyTG), boolInt(account.NotifyWebhook),
		string(cred), string(opts), formatTime(account.CreatedAt), formatTime(account.UpdatedAt))
	return err
}

func (s *SQLiteStore) DeleteAccount(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM accounts WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) SaveRecord(ctx context.Context, result domain.SignInResult) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO signin_records(
			id, account_id, platform, account_name, trigger, success, message, mode, username, nickname, email,
			signin_days, reward_points, total_points, raw, started_at, finished_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, result.ID, result.AccountID, result.Platform, result.AccountName, result.Trigger, boolInt(result.Success), result.Message, result.Mode,
		result.Username, result.Nickname, result.Email, result.SigninDays, result.RewardPoints, result.TotalPoints, result.Raw,
		formatTime(result.StartedAt), formatTime(result.FinishedAt))
	return err
}

func (s *SQLiteStore) ListRecords(ctx context.Context, filter domain.RecordFilter) ([]domain.SignInResult, error) {
	query := `SELECT id, account_id, platform, account_name, trigger, success, message, mode, username, nickname, email, signin_days, reward_points, total_points, raw, started_at, finished_at FROM signin_records`
	var args []any
	var where []string
	if strings.TrimSpace(filter.AccountID) != "" {
		where = append(where, "account_id = ?")
		args = append(args, strings.TrimSpace(filter.AccountID))
	}
	if strings.TrimSpace(filter.Platform) != "" {
		where = append(where, "platform = ?")
		args = append(args, strings.TrimSpace(filter.Platform))
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY finished_at DESC LIMIT ?"
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []domain.SignInResult
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *SQLiteStore) HasSuccessToday(ctx context.Context, accountID string, loc *time.Location) (bool, error) {
	if loc == nil {
		loc = time.Local
	}
	now := time.Now().In(loc)
	startLocal := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	endLocal := startLocal.Add(24 * time.Hour)
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(1) FROM signin_records
		WHERE account_id = ? AND success = 1 AND finished_at >= ? AND finished_at < ?
	`, accountID, formatTime(startLocal.UTC()), formatTime(endLocal.UTC())).Scan(&count)
	return count > 0, err
}

type accountScanner interface {
	Scan(dest ...any) error
}

func scanAccount(row accountScanner) (domain.Account, error) {
	var acc domain.Account
	var enabled, notifyTG, notifyWebhook int
	var credentialJSON, optionsJSON, createdAt, updatedAt string
	err := row.Scan(&acc.ID, &acc.Platform, &acc.Name, &enabled, &acc.Cron, &notifyTG, &notifyWebhook, &credentialJSON, &optionsJSON, &createdAt, &updatedAt)
	if err != nil {
		return domain.Account{}, err
	}
	acc.Enabled = enabled == 1
	acc.NotifyTG = notifyTG == 1
	acc.NotifyWebhook = notifyWebhook == 1
	if err := json.Unmarshal([]byte(credentialJSON), &acc.Credential); err != nil {
		return domain.Account{}, err
	}
	if err := json.Unmarshal([]byte(optionsJSON), &acc.Options); err != nil {
		return domain.Account{}, err
	}
	acc.CreatedAt = parseTime(createdAt)
	acc.UpdatedAt = parseTime(updatedAt)
	return acc, nil
}

type recordScanner interface {
	Scan(dest ...any) error
}

func scanRecord(row recordScanner) (domain.SignInResult, error) {
	var r domain.SignInResult
	var success int
	var startedAt, finishedAt string
	err := row.Scan(&r.ID, &r.AccountID, &r.Platform, &r.AccountName, &r.Trigger, &success, &r.Message, &r.Mode,
		&r.Username, &r.Nickname, &r.Email, &r.SigninDays, &r.RewardPoints, &r.TotalPoints, &r.Raw, &startedAt, &finishedAt)
	if err != nil {
		return domain.SignInResult{}, err
	}
	r.Success = success == 1
	r.StartedAt = parseTime(startedAt)
	r.FinishedAt = parseTime(finishedAt)
	return r, nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err == nil {
		return t
	}
	t, err = time.Parse("2006-01-02 15:04:05", raw)
	if err == nil {
		return t
	}
	return time.Time{}
}

func ValidateWritable(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("数据库路径不能为空")
	}
	return nil
}
