package emailconnection

import (
	"bytes"
	"context"
	"errors"

	"careerhub.local/backend/internal/datahome"
	"careerhub.local/backend/internal/emailcredentials"
	"careerhub.local/backend/internal/runtimepaths"
	"careerhub.local/backend/internal/secretstore"
)

// boundSecrets borrows the owner's held Root. Every external effect rechecks
// the physical scope, Home and exact pending journal (or its absence). Unbound
// and foreign pointers cannot reach the backend, even through a wrong caller.
type boundSecrets struct {
	ctx    context.Context
	root   *runtimepaths.Root
	home   datahome.Info
	scope  runtimepaths.CredentialScope
	intent []byte
	store  *secretstore.Store
}

func newBoundSecrets(ctx context.Context, root *runtimepaths.Root, home datahome.Info, backend secretstore.Backend, intent []byte) (*boundSecrets, error) {
	scope, e := root.CredentialScope()
	if e != nil {
		return nil, failure("EMAIL_CREDENTIAL_SCOPE_UNAVAILABLE")
	}
	store, err := secretstore.New(home.Channel, backend)
	if err != nil {
		return nil, err
	}
	return &boundSecrets{ctx: ctx, root: root, home: home, scope: scope, intent: bytes.Clone(intent), store: store}, nil
}

func (s *boundSecrets) check(name string) error {
	if s == nil || s.ctx == nil || s.ctx.Err() != nil {
		return failure("EMAIL_CREDENTIAL_OPERATION_CANCELED")
	}
	current, e := s.root.CredentialScope()
	if e != nil || !s.scope.Equal(current) {
		return failure("EMAIL_CREDENTIAL_SCOPE_CHANGED")
	}
	if err := emailcredentials.ValidateScopedTarget(s.home, current, name); err != nil {
		return err
	}
	home, admission := datahome.InspectOwnedForEmailCredentialRecovery(s.root, nil)
	if admission != nil || home == nil || *home != s.home {
		return failure("EMAIL_CREDENTIAL_HOME_CHANGED")
	}
	raw, present, e := s.root.ReadSingleLinkFile(datahome.EmailCredentialRecoveryPath, emailcredentials.MaxIntentBytes)
	if e != nil || present != (s.intent != nil) || !bytes.Equal(raw, s.intent) {
		return failure("EMAIL_CREDENTIAL_INTENT_CHANGED")
	}
	if present {
		if _, err := emailcredentials.DecodeScopedIntent(raw, s.home, current); err != nil {
			return err
		}
	}
	return nil
}

func (s *boundSecrets) Get(name string) (string, bool, error) {
	if err := s.check(name); err != nil {
		return "", false, err
	}
	value, found, err := s.store.Get(name)
	if checked := s.check(name); checked != nil {
		return "", false, checked
	}
	return value, found, err
}

func (s *boundSecrets) Set(name, value string) error {
	if err := s.check(name); err != nil {
		return err
	}
	err := s.store.Set(name, value)
	if checked := s.check(name); checked != nil {
		return checked
	}
	return err
}

func (s *boundSecrets) Delete(name string) (bool, error) {
	if err := s.check(name); err != nil {
		return false, err
	}
	deleted, err := s.store.Delete(name)
	if checked := s.check(name); checked != nil {
		return deleted, checked
	}
	return deleted, err
}

// Read-only preflight observation. Publication re-observes under both gates;
// this returned value alone never authorizes store I/O.
func scopeAtPath(path string) (scope runtimepaths.CredentialScope, problem *Error) {
	root, e := runtimepaths.Open(path)
	if e != nil {
		return scope, failure("EMAIL_CREDENTIAL_PATH_UNAVAILABLE")
	}
	defer func() {
		if root.Close() != nil {
			scope = runtimepaths.CredentialScope{}
			problem = failure("EMAIL_CREDENTIAL_OPERATION_UNCONFIRMED")
		}
	}()
	scope, e = root.CredentialScope()
	if e != nil {
		return runtimepaths.CredentialScope{}, failure("EMAIL_CREDENTIAL_SCOPE_UNAVAILABLE")
	}
	return scope, nil
}

func boundTargetProblem(root *runtimepaths.Root, home datahome.Info, name string) *Error {
	scope, e := root.CredentialScope()
	if e != nil {
		return failure("EMAIL_CREDENTIAL_SCOPE_UNAVAILABLE")
	}
	reconnect, err := emailcredentials.RequiresReauthorization(home, scope, name)
	if err != nil {
		return failure("EMAIL_CONNECTION_INVALID")
	}
	if reconnect {
		return failure("EMAIL_CREDENTIAL_RECONNECT_REQUIRED")
	}
	return nil
}

func nonDeletingOperation(operation string) bool {
	return operation == emailcredentials.AdoptUnbound || operation == emailcredentials.RebindCopied
}

func credentialStoreProblem(err error) *Error {
	var local *Error
	if errors.As(err, &local) {
		return local
	}
	for _, code := range []error{emailcredentials.ErrInvalidTarget, emailcredentials.ErrIntentInvalid, emailcredentials.ErrIntentHomeChanged, emailcredentials.ErrIntentScopeChanged} {
		if errors.Is(err, code) {
			return failure(code.Error())
		}
	}
	return failure("EMAIL_SECRET_STORE_FAILED")
}
