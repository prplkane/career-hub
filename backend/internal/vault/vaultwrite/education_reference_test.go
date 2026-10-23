package vaultwrite

import ()

type recordEditStep struct {
	Target, Op, ID  string
	Payload         map[string]any
	Stale, Archived bool
}
