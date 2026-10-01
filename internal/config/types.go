package config

import (
	"context"
	"fmt"
)

const (
	ModeWorkspace = "workspace"
	ModeReadonly  = "readonly"
	ModeBenchmark = "benchmark"

	OpenRouterCredentialTarget = "Brunel/OpenRouter"
)

type Config struct {
	Mode    string `json:"mode"`
	ModelID string `json:"model_id"`
}

// CLIOverrides uses pointers so an omitted flag and an explicitly empty value
// remain distinguishable. Explicit empty values are rejected during loading.
type CLIOverrides struct {
	Mode    *string
	ModelID *string
}

type Resolved struct {
	Config Config `json:"config"`
	apiKey string
}

func (r Resolved) String() string {
	return fmt.Sprintf("mode=%s model_id=%s", r.Config.Mode, r.Config.ModelID)
}

func (r Resolved) GoString() string { return r.String() }

func (r Resolved) OpenRouterAPIKey() string { return r.apiKey }

type CredentialSource interface {
	OpenRouterAPIKey(context.Context) (string, error)
}

type CredentialWriter interface {
	SetOpenRouterAPIKey(string) error
	// DeleteOpenRouterAPIKey removes the stored key. It returns
	// ErrCredentialNotFound when there is nothing to remove.
	DeleteOpenRouterAPIKey() error
}

type Loader struct {
	WorkspaceRoot string
	UserProfile   string
	Credentials   CredentialSource
	Defaults      Config
}

func NewLoader(workspaceRoot, userProfile string, credentials CredentialSource) *Loader {
	if credentials == nil {
		credentials = NewPlatformCredentialSource()
	}
	defaults := Config{Mode: ModeWorkspace}
	return &Loader{WorkspaceRoot: workspaceRoot, UserProfile: userProfile, Credentials: credentials, Defaults: defaults}
}
