package main

import (
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/providerfence"
)

const (
	providerFenceIdentityKeyFile       = providerfence.IdentityKeyFile
	providerFenceIdentityKeyDigestFile = providerfence.IdentityKeyDigestFile
)

func providerUsageFenceIdentityForCity(cityPath string, resolved *config.ResolvedProvider, accountEnv map[string]string) (string, error) {
	return providerfence.IdentityForCity(cityPath, resolved, accountEnv)
}

func providerUsageFenceIdentityForCityWithStore(cityPath string, store beads.Store, resolved *config.ResolvedProvider, accountEnv map[string]string) (string, error) {
	return providerfence.IdentityForCityWithStore(cityPath, store, resolved, accountEnv)
}

func providerFenceAccountEnv(env map[string]string) map[string]string {
	return providerfence.AccountEnv(env)
}
