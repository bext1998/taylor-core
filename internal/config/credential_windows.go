//go:build windows

package config

import (
	"context"
	"errors"
	"strings"
	"syscall"
	"unicode/utf8"
	"unsafe"
)

const credentialTypeGeneric = 1

// errorNotFound is ERROR_NOT_FOUND, what CredReadW reports when the target
// has no stored credential.
const errorNotFound = syscall.Errno(1168)

type credential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        syscall.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

var (
	advapi32   = syscall.NewLazyDLL("advapi32.dll")
	credRead   = advapi32.NewProc("CredReadW")
	credWrite  = advapi32.NewProc("CredWriteW")
	credDelete = advapi32.NewProc("CredDeleteW")
	credFree   = advapi32.NewProc("CredFree")
)

type platformCredentialSource struct{}

func NewPlatformCredentialSource() CredentialSource { return platformCredentialSource{} }

func (platformCredentialSource) OpenRouterAPIKey(context.Context) (string, error) {
	target, err := syscall.UTF16PtrFromString(OpenRouterCredentialTarget)
	if err != nil {
		return "", err
	}
	var pointer *credential
	result, _, callErr := credRead.Call(
		uintptr(unsafe.Pointer(target)),
		credentialTypeGeneric,
		0,
		uintptr(unsafe.Pointer(&pointer)),
	)
	if result == 0 {
		if errors.Is(callErr, errorNotFound) {
			return "", ErrCredentialNotFound
		}
		return "", callErr
	}
	defer credFree.Call(uintptr(unsafe.Pointer(pointer)))
	if pointer == nil || pointer.CredentialBlob == nil || pointer.CredentialBlobSize == 0 {
		return "", errors.New("Credential Manager credential is empty")
	}
	return decodeCredentialBlob(unsafe.Slice(pointer.CredentialBlob, pointer.CredentialBlobSize))
}

type platformCredentialWriter struct{}

func NewPlatformCredentialWriter() CredentialWriter { return platformCredentialWriter{} }

func (platformCredentialWriter) SetOpenRouterAPIKey(key string) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("OpenRouter credential is empty")
	}
	if strings.IndexByte(key, 0) >= 0 {
		return errors.New("OpenRouter credential contains NUL")
	}
	if !utf8.ValidString(key) {
		return errors.New("OpenRouter credential is not valid UTF-8")
	}
	if uint64(len(key)) > uint64(^uint32(0)) {
		return errors.New("OpenRouter credential is too large")
	}
	target, err := syscall.UTF16PtrFromString(OpenRouterCredentialTarget)
	if err != nil {
		return err
	}
	blob := []byte(key)
	value := credential{
		Type:               credentialTypeGeneric,
		TargetName:         target,
		CredentialBlobSize: uint32(len(blob)),
		CredentialBlob:     &blob[0],
		Persist:            2,
	}
	result, _, callErr := credWrite.Call(uintptr(unsafe.Pointer(&value)), 0)
	if result == 0 {
		return callErr
	}
	return nil
}

func (platformCredentialWriter) DeleteOpenRouterAPIKey() error {
	target, err := syscall.UTF16PtrFromString(OpenRouterCredentialTarget)
	if err != nil {
		return err
	}
	result, _, callErr := credDelete.Call(uintptr(unsafe.Pointer(target)), credentialTypeGeneric, 0)
	if result == 0 {
		if errors.Is(callErr, errorNotFound) {
			return ErrCredentialNotFound
		}
		return callErr
	}
	return nil
}
