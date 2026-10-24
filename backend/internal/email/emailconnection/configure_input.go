package emailconnection

import (
	"net/mail"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"careerhub.local/backend/internal/email/emailrouting"
	"careerhub.local/backend/internal/storage/datahome"
	"careerhub.local/backend/internal/storage/secretstore"
	"careerhub.local/backend/internal/textcompat"
)

const (
	maxConnectionSenders = 100
	maxAddressRunes      = 320
	defaultInterval      = 30
)

// ConfigureNewInput is deliberately typed and dedicated to the no-row case.
// A later route must parse untrusted JSON before calling the native mutator.
// Password is transient and must never be logged or written to SQLite/intent.
type ConfigureNewInput struct {
	// ExpectedHome is supplied by an already-admitted sidecar, never by JSON.
	// Nil retains the existing native-only call contract.
	ExpectedHome       *datahome.Info
	AccountAddress     string
	Password           string
	JobSenders         []string
	ApplicationSenders []string
	// Nil applies the permissive policy to every sender. A non-nil map must
	// cover exactly the normalized sender set. Values are bounded policy JSON.
	SubjectPolicies map[string]string
	ScheduleEnabled bool
	// Nil uses the source default; an explicit value must be 15..1440.
	IntervalMinutes  *int
	ExpectedRevision int64
}

type normalizedSender struct {
	address, purpose, policyJSON string
}

type normalizedNewConnection struct {
	account, password string
	senders           []normalizedSender
	scheduled         bool
	interval          int
}

func normalizeNewConnection(input ConfigureNewInput) (normalizedNewConnection, *Error) {
	return normalizeConnectionFields(input, true)
}

// normalizeConnectionFields is shared by initial configure and no-password
// settings edits. Only the initial path accepts a transient password and a
// zero expected revision; the caller validates edit revision separately.
func normalizeConnectionFields(input ConfigureNewInput, initial bool) (normalizedNewConnection, *Error) {
	var out normalizedNewConnection
	var valid bool
	out.account, valid = normalizeConnectionAddress(input.AccountAddress)
	if !valid {
		return out, failure("EMAIL_CONNECTION_INVALID")
	}
	if initial {
		var passwordProblem *Error
		out.password, passwordProblem = normalizeRequiredPassword(input.Password)
		if passwordProblem != nil {
			return normalizedNewConnection{}, passwordProblem
		}
		if input.ExpectedRevision < 0 {
			return normalizedNewConnection{}, failure("EMAIL_CONNECTION_INVALID")
		}
		if input.ExpectedRevision != 0 {
			return normalizedNewConnection{}, failure("EMAIL_CONNECTION_REVISION_CONFLICT")
		}
	}
	out.interval = defaultInterval
	if input.IntervalMinutes != nil {
		out.interval = *input.IntervalMinutes
	}
	if out.interval < 15 || out.interval > 1440 {
		return normalizedNewConnection{}, failure("EMAIL_CONNECTION_INVALID")
	}
	out.scheduled = input.ScheduleEnabled
	purposes := make(map[string]string)
	for _, raw := range input.JobSenders {
		address, valid := normalizeConnectionAddress(raw)
		if !valid {
			return normalizedNewConnection{}, failure("EMAIL_CONNECTION_INVALID")
		}
		purposes[address] = emailrouting.SenderPurposeJobAlerts
		if len(purposes) > maxConnectionSenders {
			return normalizedNewConnection{}, failure("EMAIL_CONNECTION_INVALID")
		}
	}
	for _, raw := range input.ApplicationSenders {
		address, valid := normalizeConnectionAddress(raw)
		if !valid {
			return normalizedNewConnection{}, failure("EMAIL_CONNECTION_INVALID")
		}
		if purposes[address] == emailrouting.SenderPurposeJobAlerts {
			purposes[address] = emailrouting.SenderPurposeBoth
		} else {
			purposes[address] = emailrouting.SenderPurposeApplicationTracking
		}
		if len(purposes) > maxConnectionSenders {
			return normalizedNewConnection{}, failure("EMAIL_CONNECTION_INVALID")
		}
	}
	if len(purposes) == 0 || len(purposes) > maxConnectionSenders {
		return normalizedNewConnection{}, failure("EMAIL_CONNECTION_INVALID")
	}
	policies := make(map[string]emailrouting.Policy, len(purposes))
	if input.SubjectPolicies != nil {
		if len(input.SubjectPolicies) != len(purposes) {
			return normalizedNewConnection{}, failure("EMAIL_CONNECTION_INVALID")
		}
		for raw, encoded := range input.SubjectPolicies {
			address, valid := normalizeConnectionAddress(raw)
			if !valid || purposes[address] == "" {
				return normalizedNewConnection{}, failure("EMAIL_CONNECTION_INVALID")
			}
			if _, duplicate := policies[address]; duplicate {
				return normalizedNewConnection{}, failure("EMAIL_CONNECTION_INVALID")
			}
			policy, err := emailrouting.ParsePolicyJSON(encoded)
			if err != nil {
				return normalizedNewConnection{}, failure("EMAIL_CONNECTION_INVALID")
			}
			policies[address] = policy
		}
		if len(policies) != len(purposes) {
			return normalizedNewConnection{}, failure("EMAIL_CONNECTION_INVALID")
		}
	}
	addresses := make([]string, 0, len(purposes))
	for address := range purposes {
		addresses = append(addresses, address)
	}
	slices.Sort(addresses)
	for _, address := range addresses {
		policy := emailrouting.EmptyPolicy()
		if input.SubjectPolicies != nil {
			policy = policies[address]
		}
		out.senders = append(out.senders, normalizedSender{address, purposes[address], policy.CanonicalJSON()})
	}
	return out, nil
}

func normalizeConnectionAddress(raw string) (string, bool) {
	if !utf8.ValidString(raw) {
		return "", false
	}
	address := textcompat.Fold(strings.TrimSpace(raw))
	if address == "" || utf8.RuneCountInString(address) > maxAddressRunes || strings.Count(address, "@") != 1 {
		return "", false
	}
	for _, r := range address {
		if r == '<' || r == '>' || unicode.IsSpace(r) || unicode.IsControl(r) {
			return "", false
		}
	}
	parsed, err := mail.ParseAddress(address)
	return address, err == nil && parsed.Name == "" && parsed.Address == address
}

func normalizeConnectionPassword(raw string) string {
	password := strings.TrimSpace(raw)
	compact := strings.ReplaceAll(password, " ", "")
	if len(compact) == 16 {
		asciiAlnum := true
		for i := range len(compact) {
			c := compact[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
				asciiAlnum = false
				break
			}
		}
		if asciiAlnum {
			return compact
		}
	}
	return password
}

func normalizeRequiredPassword(raw string) (string, *Error) {
	password := normalizeConnectionPassword(raw)
	if password == "" {
		return "", failure("EMAIL_CREDENTIAL_MISSING")
	}
	if !utf8.ValidString(password) || len(password) > secretstore.MaxSecretBytes {
		return "", failure("EMAIL_CONNECTION_INVALID")
	}
	return password, nil
}
