package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfig_GetFallbackChain(t *testing.T) {
	t.Run("model-level fallback returned when set", func(t *testing.T) {
		config := Config{
			Models: map[string]ModelConfig{
				"model-a": {
					Fallback: []string{"model-b", "model-c"},
				},
			},
			Fallbacks: make(map[string][]string),
		}
		chain := config.GetFallbackChain("model-a")
		assert.Equal(t, []string{"model-b", "model-c"}, chain)
	})

	t.Run("top-level fallbacks used when no model-level", func(t *testing.T) {
		config := Config{
			Models: map[string]ModelConfig{
				"model-a": {},
			},
			Fallbacks: map[string][]string{
				"model-a": {"from-top-level"},
			},
		}
		chain := config.GetFallbackChain("model-a")
		assert.Equal(t, []string{"from-top-level"}, chain)
	})

	t.Run("model-level overrides top-level", func(t *testing.T) {
		config := Config{
			Models: map[string]ModelConfig{
				"model-a": {
					Fallback: []string{"from-model-level"},
				},
			},
			Fallbacks: map[string][]string{
				"model-a": {"from-top-level"},
			},
		}
		// But GetFallbackChain should return the model-level value
		assert.Equal(t, []string{"from-model-level"}, config.GetFallbackChain("model-a"))
	})

	t.Run("nil returned when neither set", func(t *testing.T) {
		config := Config{
			Models:    map[string]ModelConfig{"model-a": {}},
			Fallbacks: make(map[string][]string),
		}
		chain := config.GetFallbackChain("model-a")
		assert.Nil(t, chain)
	})

	t.Run("peer model name with only top-level entry works", func(t *testing.T) {
		config := Config{
			Models: make(map[string]ModelConfig),
			Fallbacks: map[string][]string{
				"gpt-4o": {"local-llama", "backup-peer"},
			},
		}
		chain := config.GetFallbackChain("gpt-4o")
		assert.Equal(t, []string{"local-llama", "backup-peer"}, chain)
	})

	t.Run("alias resolves to real model for fallback lookup", func(t *testing.T) {
		config := Config{
			Models: map[string]ModelConfig{
				"real-model": {
					Aliases:  []string{"short-alias"},
					Fallback: []string{"backup"},
				},
			},
			Fallbacks: make(map[string][]string),
			aliases:   map[string]string{"short-alias": "real-model"},
		}
		// Resolve alias to real name first
		realName, _ := config.RealModelName("short-alias")
		chain := config.GetFallbackChain(realName)
		assert.Equal(t, []string{"backup"}, chain)
	})
}
