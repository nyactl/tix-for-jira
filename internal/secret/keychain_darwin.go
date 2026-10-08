package secret

import (
	"errors"
	"fmt"

	"github.com/keybase/go-keychain"
)

const service = "tix-jira"

// Keychain stores tokens as generic passwords in the login keychain.
//
// The item is created by this binary, so the keychain's access list trusts
// only this binary: other programs, including `security
// find-generic-password`, make macOS ask the user first. The item is not
// synchronised to iCloud. Accessibility attributes are deliberately left
// unset; setting them moves the item to the data protection keychain, which
// requires code-signing entitlements an unsigned CLI does not have.
type Keychain struct{}

func NewKeychain() Store { return Keychain{} }

func (Keychain) Get(account string) (string, error) {
	data, err := keychain.GetGenericPassword(service, account, "", "")
	if errors.Is(err, keychain.ErrorItemNotFound) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("reading token from Keychain: %w", err)
	}
	if data == nil {
		return "", ErrNotFound
	}
	return string(data), nil
}

func (k Keychain) Set(account, token string) error {
	if err := k.Delete(account); err != nil {
		return err
	}
	item := keychain.NewItem()
	item.SetSecClass(keychain.SecClassGenericPassword)
	item.SetService(service)
	item.SetAccount(account)
	item.SetLabel("tix-jira API token (" + account + ")")
	item.SetData([]byte(token))
	item.SetSynchronizable(keychain.SynchronizableNo)
	if err := keychain.AddItem(item); err != nil {
		return fmt.Errorf("saving token to Keychain: %w", err)
	}
	return nil
}

func (Keychain) Delete(account string) error {
	err := keychain.DeleteGenericPasswordItem(service, account)
	if err != nil && !errors.Is(err, keychain.ErrorItemNotFound) {
		return fmt.Errorf("removing token from Keychain: %w", err)
	}
	return nil
}
