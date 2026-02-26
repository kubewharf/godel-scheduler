/*
Copyright 2023 The Godel Scheduler Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package options

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kubewharf/godel-scheduler/pkg/binder"
)

// ---------------------------------------------------------------------------
// Fallback / config switch tests — verify that toggling --enable-embedded-binder
// between true and false is sufficient to switch between embedded and standalone
// Binder modes without changing any other configuration.
// ---------------------------------------------------------------------------

// TestEmbeddedBinderFallback_DisabledByDefault verifies that when no embedded
// binder flags are specified, the feature is disabled.
func TestEmbeddedBinderFallback_DisabledByDefault(t *testing.T) {
	opts, err := NewOptions()
	require.NoError(t, err)

	assert.False(t, opts.EnableEmbeddedBinder,
		"embedded binder should be disabled by default")
}

// TestEmbeddedBinderFallback_EnableAndDisableSwitch verifies that toggling the
// flag between true/false is safe and self-consistent.
func TestEmbeddedBinderFallback_EnableAndDisableSwitch(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		expect bool
	}{
		{"enable via flag", []string{"--enable-embedded-binder=true"}, true},
		{"disable via flag", []string{"--enable-embedded-binder=false"}, false},
		{"implicit true", []string{"--enable-embedded-binder"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := NewOptions()
			require.NoError(t, err)
			nfs := opts.Flags()
			fs := nfs.FlagSet("embedded binder")
			err = fs.Parse(tt.args)
			require.NoError(t, err)
			assert.Equal(t, tt.expect, opts.EnableEmbeddedBinder)
		})
	}
}

// TestEmbeddedBinderFallback_ConfigPropagation verifies that non-default config
// values are propagated correctly when the flag is toggled on vs. off. When
// disabled, the config is still stored on Options (safe to keep); server.go
// simply skips instantiating the EmbeddedBinder.
func TestEmbeddedBinderFallback_ConfigPropagation(t *testing.T) {
	opts, err := NewOptions()
	require.NoError(t, err)
	opts.EnableEmbeddedBinder = false
	opts.EmbeddedBinderConfig.MaxBindRetries = 99
	opts.EmbeddedBinderConfig.MaxLocalRetries = 42

	assert.False(t, opts.EnableEmbeddedBinder)
	assert.Equal(t, 99, opts.EmbeddedBinderConfig.MaxBindRetries,
		"non-default config should be preserved even when disabled")
	assert.Equal(t, 42, opts.EmbeddedBinderConfig.MaxLocalRetries)
}

// TestEmbeddedBinderFallback_DefaultConfigValues verifies that the default
// embedded binder config matches the expected production defaults.
func TestEmbeddedBinderFallback_DefaultConfigValues(t *testing.T) {
	opts, err := NewOptions()
	require.NoError(t, err)

	assert.Equal(t, binder.DefaultMaxBindRetries, opts.EmbeddedBinderConfig.MaxBindRetries)
	assert.Equal(t, binder.DefaultBindTimeout, opts.EmbeddedBinderConfig.BindTimeout)
	assert.Equal(t, binder.DefaultMaxLocalRetries, opts.EmbeddedBinderConfig.MaxLocalRetries)
}

// TestEmbeddedBinderFallback_ValidationOnlyWhenEnabled verifies that validation
// of EmbeddedBinderConfig is enforced only when the feature is enabled.
func TestEmbeddedBinderFallback_ValidationOnlyWhenEnabled(t *testing.T) {
	opts, err := NewOptions()
	require.NoError(t, err)

	// Set invalid config but keep feature disabled — should pass validation
	opts.EnableEmbeddedBinder = false
	opts.EmbeddedBinderConfig.MaxBindRetries = -1

	errs := opts.Validate()
	assert.Empty(t, errs, "invalid config should not cause validation failure when feature is disabled")

	// Enable the feature with the same invalid config — should fail validation
	opts.EnableEmbeddedBinder = true
	errs = opts.Validate()
	assert.NotEmpty(t, errs, "invalid config should cause validation failure when feature is enabled")
}
