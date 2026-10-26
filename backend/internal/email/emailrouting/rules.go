package emailrouting

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"careerhub.local/backend/internal/textcompat"
)

const maxSenderRules = 100

var ErrRulesUnavailable = errors.New("EMAIL_SYNC_LOCAL_WRITE_FAILED")

// Rule is one approved exact-sender route. A Policy cannot be constructed
// without the bounded parser, so a stored malformed policy fails closed.
type Rule struct {
	Purpose string
	Policy  Policy
}

func storedAddress(value string) bool {
	if value == "" || value != textcompat.Fold(value) || utf8.RuneCountInString(value) > 320 ||
		strings.Count(value, "@") != 1 || strings.HasPrefix(value, "@") || strings.HasSuffix(value, "@") {
		return false
	}
	for _, r := range value {
		if r == '<' || r == '>' || textcompat.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// LoadAllowed reads the exact approved sender map from an already-owned
// current-schema transaction. It never widens trust to a domain, disabled
// rule, malformed address/purpose, or malformed subject policy.
func LoadAllowed(ctx context.Context, tx *sql.Tx) (map[string]Rule, error) {
	if ctx == nil || tx == nil {
		return nil, ErrInvalidPolicy
	}
	if ctx.Err() != nil {
		return nil, ErrRulesUnavailable
	}
	rows, err := tx.QueryContext(ctx, `SELECT sender_address, purpose, subject_policy_json
		FROM job_email_sender_rules WHERE connection_id = ? AND disposition = 'allowed'
		ORDER BY sender_address`, "primary")
	if err != nil {
		return nil, ErrRulesUnavailable
	}
	defer rows.Close()
	rules := make(map[string]Rule)
	for rows.Next() {
		var address, purpose, rawPolicy string
		if err := rows.Scan(&address, &purpose, &rawPolicy); err != nil {
			return nil, ErrRulesUnavailable
		}
		if !storedAddress(address) || len(rules) >= maxSenderRules {
			return nil, ErrInvalidPolicy
		}
		switch purpose {
		case SenderPurposeJobAlerts, SenderPurposeApplicationTracking, SenderPurposeBoth:
		default:
			return nil, ErrInvalidPolicy
		}
		policy, err := ParsePolicyJSON(rawPolicy)
		if err != nil {
			return nil, ErrInvalidPolicy
		}
		rules[address] = Rule{Purpose: purpose, Policy: policy}
	}
	if rows.Err() != nil || rows.Close() != nil {
		return nil, ErrRulesUnavailable
	}
	return rules, nil
}
