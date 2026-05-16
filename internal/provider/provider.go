package provider

import (
	"context"

	"qiandao/internal/domain"
)

type Result struct {
	SignInResult    domain.SignInResult
	CredentialPatch *domain.AccountCredential
}

type Provider interface {
	Platform() string
	SignIn(ctx context.Context, account domain.Account, settings domain.Settings) (Result, error)
}
