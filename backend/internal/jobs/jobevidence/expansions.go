package jobevidence

import (
	"context"
	"strings"
	"unicode/utf8"

	"careerhub.local/backend/internal/skills/catalog"
)

// BuildExpansions preserves depth-first descendant order and first-owner alias
// keys. Callers supply the effective active catalog; this pure source helper
// deliberately does not reinterpret archive flags or validate catalog graphs.
func BuildExpansions(ctx context.Context, terms []catalog.Term) (map[string][]string, *Error) {
	if err := stopped(ctx); err != nil {
		return nil, err
	}
	if len(terms) > MaxSources {
		return nil, &Error{Code: "EVIDENCE_EXPANSION_LIMIT"}
	}
	byID, children := map[string]catalog.Term{}, map[string][]string{}
	inputBytes, nodes := 0, 0
	for _, term := range terms {
		if err := stopped(ctx); err != nil {
			return nil, err
		}
		texts := []string{term.ID, term.Name}
		if term.Parent != nil {
			texts = append(texts, *term.Parent)
		}
		if len(term.Aliases) > MaxSources {
			return nil, &Error{Code: "EVIDENCE_EXPANSION_LIMIT"}
		}
		texts = append(texts, term.Aliases...)
		for _, text := range texts {
			inputBytes += len(text)
			nodes++
			if inputBytes > MaxIndexBytes || nodes > maxNodes {
				return nil, &Error{Code: "EVIDENCE_EXPANSION_LIMIT"}
			}
			if !utf8.ValidString(text) {
				return nil, &Error{Code: "EVIDENCE_TEXT_INVALID"}
			}
		}
		byID[term.ID] = term
		if term.Parent != nil && *term.Parent != "" {
			children[*term.Parent] = append(children[*term.Parent], term.ID)
		}
	}
	output, outputBytes := map[string][]string{}, 0
	for _, term := range terms {
		seen := map[string]bool{term.ID: true}
		// Iterative DFS avoids an unbounded native stack on long catalog chains.
		stack := [][]string{children[term.ID]}
		extra := []string{}
		for len(stack) != 0 {
			if err := stopped(ctx); err != nil {
				return nil, err
			}
			last := len(stack) - 1
			if len(stack[last]) == 0 {
				stack = stack[:last]
				continue
			}
			id := stack[last][0]
			stack[last] = stack[last][1:]
			nodes++
			if nodes > maxNodes {
				return nil, &Error{Code: "EVIDENCE_EXPANSION_LIMIT"}
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			if child, exists := byID[id]; exists {
				texts := append([]string{child.Name}, child.Aliases...)
				for _, text := range texts {
					outputBytes += len(text)
					nodes++
					if outputBytes > MaxIndexBytes || len(extra) >= MaxSources || nodes > maxNodes {
						return nil, &Error{Code: "EVIDENCE_EXPANSION_LIMIT"}
					}
					extra = append(extra, text)
				}
			}
			stack = append(stack, children[id])
		}
		if len(extra) == 0 {
			continue
		}
		for _, name := range append([]string{term.Name}, term.Aliases...) {
			key, err := Normalize(ctx, name)
			if err != nil {
				return nil, err
			}
			if _, exists := output[key]; !exists {
				outputBytes += len(key)
				if outputBytes > MaxIndexBytes || len(output) >= MaxSources {
					return nil, &Error{Code: "EVIDENCE_EXPANSION_LIMIT"}
				}
				output[key] = extra
			}
		}
	}
	if err := stopped(ctx); err != nil {
		return nil, err
	}
	return output, nil
}

func TechnologyGroupLabel(ctx context.Context, technologies []string) (string, *Error) {
	if err := stopped(ctx); err != nil {
		return "", err
	}
	if len(technologies) > MaxSources {
		return "", &Error{Code: "EVIDENCE_INPUT_LIMIT"}
	}
	bytes := len("Any of: ")
	for _, name := range technologies {
		if !utf8.ValidString(name) {
			return "", &Error{Code: "EVIDENCE_TEXT_INVALID"}
		}
		bytes += len(name) + 3
		if bytes > MaxTextBytes {
			return "", &Error{Code: "EVIDENCE_TEXT_LIMIT"}
		}
	}
	if err := stopped(ctx); err != nil {
		return "", err
	}
	return "Any of: " + strings.Join(technologies, " | "), nil
}
