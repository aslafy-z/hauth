// Package all wires the production adapters into the registry.
package all

import (
	"github.com/aslafy-z/hauth/internal/adapter"
	"github.com/aslafy-z/hauth/internal/adapter/agy"
	"github.com/aslafy-z/hauth/internal/adapter/claude"
	"github.com/aslafy-z/hauth/internal/adapter/codex"
	"github.com/aslafy-z/hauth/internal/adapter/common"
	"github.com/aslafy-z/hauth/internal/adapter/gh"
	"github.com/aslafy-z/hauth/internal/adapter/opencode"
)

// Register registers the five production adapters. The fake adapter is
// wired separately by tests and demo mode.
func Register(cfg common.Config) {
	adapter.Register(gh.New(cfg))
	adapter.Register(claude.New(cfg))
	adapter.Register(codex.New(cfg))
	adapter.Register(opencode.New(cfg))
	adapter.Register(agy.New(cfg))
}
