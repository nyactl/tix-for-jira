//go:build !darwin

package secret

import "errors"

type unsupported struct{}

// NewKeychain returns a store that always fails: tix-for-jira keeps its token
// in the macOS Keychain and supports no other secret storage.
func NewKeychain() Store { return unsupported{} }

var errUnsupported = errors.New("token storage is only supported on macOS (Keychain)")

func (unsupported) Get(string) (string, error) { return "", errUnsupported }
func (unsupported) Set(string, string) error   { return errUnsupported }
func (unsupported) Delete(string) error        { return errUnsupported }
