//go:build !windows

package config

import (
	"context"
)

type platformCredentialSource struct{}

type platformCredentialWriter struct{}

func NewPlatformCredentialSource() CredentialSource { return platformCredentialSource{} }

func (platformCredentialSource) OpenRouterAPIKey(context.Context) (string, error) {
	return "", ErrUnsupportedPlatform
}

func NewPlatformCredentialWriter() CredentialWriter { return platformCredentialWriter{} }

func (platformCredentialWriter) SetOpenRouterAPIKey(string) error {
	return ErrUnsupportedPlatform
}

func (platformCredentialWriter) DeleteOpenRouterAPIKey() error {
	return ErrUnsupportedPlatform
}
