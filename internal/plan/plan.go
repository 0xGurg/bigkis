// Package plan provides set-diff helpers shared by all plugins.
package plan

import "sort"

// Diff computes the additions, removals, and unchanged elements between a
// declared and an actual set. Anything in `ignored` is omitted from both
// additions and removals.
//
//	declared    - what the user wants installed
//	actual      - what is currently installed on the system
//	lastApplied - what bigkis previously declared (used to scope removals so we
//	              don't yank packages the user installed manually)
//	ignored     - never touch
type Diff struct {
	Add    []string
	Remove []string
	Keep   []string
}

// Compute computes additions and removals.
//
// Removal policy: only remove items that are present in lastApplied (i.e.
// bigkis previously declared them) but no longer declared. This protects
// packages the user installed manually outside bigkis.
//
// First-run safety: if lastApplied is nil, Compute does not produce any
// removals. The caller's first apply records what is declared but never yanks
// packages the user already had on the system. An empty non-nil slice means
// "we previously declared nothing", which can legitimately produce removals
// only if the previous declared set was non-empty (it wasn't — it was empty).
func Compute(declared, actual, lastApplied, ignored []string) Diff {
	declSet := toSet(declared)
	actSet := toSet(actual)
	ignoredSet := toSet(ignored)

	for k := range ignoredSet {
		delete(declSet, k)
	}

	var d Diff
	for name := range declSet {
		if _, ok := actSet[name]; !ok {
			d.Add = append(d.Add, name)
		} else {
			d.Keep = append(d.Keep, name)
		}
	}

	if lastApplied != nil {
		for name := range toSet(lastApplied) {
			if _, declared := declSet[name]; declared {
				continue
			}
			if _, ignored := ignoredSet[name]; ignored {
				continue
			}
			if _, installed := actSet[name]; !installed {
				continue
			}
			d.Remove = append(d.Remove, name)
		}
	}

	sort.Strings(d.Add)
	sort.Strings(d.Remove)
	sort.Strings(d.Keep)
	return d
}

// HasChanges returns true if there is anything to add or remove.
func (d Diff) HasChanges() bool {
	return len(d.Add) > 0 || len(d.Remove) > 0
}

func toSet(items []string) map[string]struct{} {
	m := make(map[string]struct{}, len(items))
	for _, x := range items {
		if x == "" {
			continue
		}
		m[x] = struct{}{}
	}
	return m
}
