package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// SortedAccountNames returns the account names in cfg in alphabetical order.
func SortedAccountNames(cfg *Config) []string {
	names := make([]string, 0, len(cfg.Accounts))
	for name := range cfg.Accounts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ResolveTargets returns the sorted names of the accounts selected by account:
// every enabled account for "all" (or ""), otherwise the single named account.
func ResolveTargets(cfg *Config, account string) ([]string, error) {
	if account == "" || strings.EqualFold(account, "all") {
		var names []string
		for _, name := range SortedAccountNames(cfg) {
			if acc := cfg.Accounts[name]; acc.Enabled == nil || *acc.Enabled {
				names = append(names, name)
			}
		}
		return names, nil
	}
	if _, ok := cfg.Accounts[account]; !ok {
		return nil, fmt.Errorf("account '%s' not found", account)
	}
	return []string{account}, nil
}

// accountResult is the outcome of running an operation against one account.
type accountResult[T any] struct {
	Name  string
	Value T
	Err   error
}

// forEachAccount runs fn for every named account concurrently and returns the
// results in the same order as names.
func forEachAccount[T any](cfg *Config, names []string, fn func(name string, acc AccountConfig) (T, error)) []accountResult[T] {
	results := make([]accountResult[T], len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Go(func() {
			v, err := fn(name, cfg.Accounts[name])
			results[i] = accountResult[T]{Name: name, Value: v, Err: err}
		})
	}
	wg.Wait()
	return results
}
