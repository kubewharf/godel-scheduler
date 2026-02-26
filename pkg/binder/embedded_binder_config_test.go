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

package binder

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDefaultEmbeddedBinderConfig(t *testing.T) {
	cfg := DefaultEmbeddedBinderConfig()

	assert.NotNil(t, cfg)
	assert.Equal(t, DefaultMaxBindRetries, cfg.MaxBindRetries, "MaxBindRetries default")
	assert.Equal(t, DefaultBindTimeout, cfg.BindTimeout, "BindTimeout default")
	assert.Equal(t, DefaultMaxLocalRetries, cfg.MaxLocalRetries, "MaxLocalRetries default")
	assert.Equal(t, DefaultQueueBacklogThreshold, cfg.QueueBacklogThreshold, "QueueBacklogThreshold default")

	// Verify the actual numeric defaults match spec.
	assert.Equal(t, 3, cfg.MaxBindRetries)
	assert.Equal(t, 30*time.Second, cfg.BindTimeout)
	assert.Equal(t, 5, cfg.MaxLocalRetries)
	assert.Equal(t, 100, cfg.QueueBacklogThreshold)
}

func TestEmbeddedBinderConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  *EmbeddedBinderConfig
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid default config",
			config:  DefaultEmbeddedBinderConfig(),
			wantErr: false,
		},
		{
			name: "valid custom config",
			config: &EmbeddedBinderConfig{
				MaxBindRetries:        5,
				BindTimeout:           60 * time.Second,
				MaxLocalRetries:       10,
				QueueBacklogThreshold: 200,
			},
			wantErr: false,
		},
		{
			name:    "nil config returns error",
			config:  nil,
			wantErr: true,
			errMsg:  "must not be nil",
		},
		{
			name: "negative MaxBindRetries returns error",
			config: &EmbeddedBinderConfig{
				MaxBindRetries:        -1,
				BindTimeout:           30 * time.Second,
				MaxLocalRetries:       5,
				QueueBacklogThreshold: 100,
			},
			wantErr: true,
			errMsg:  "MaxBindRetries must be >= 0",
		},
		{
			name: "zero BindTimeout returns error",
			config: &EmbeddedBinderConfig{
				MaxBindRetries:        3,
				BindTimeout:           0,
				MaxLocalRetries:       5,
				QueueBacklogThreshold: 100,
			},
			wantErr: true,
			errMsg:  "BindTimeout must be > 0",
		},
		{
			name: "negative BindTimeout returns error",
			config: &EmbeddedBinderConfig{
				MaxBindRetries:        3,
				BindTimeout:           -1 * time.Second,
				MaxLocalRetries:       5,
				QueueBacklogThreshold: 100,
			},
			wantErr: true,
			errMsg:  "BindTimeout must be > 0",
		},
		{
			name: "negative MaxLocalRetries returns error",
			config: &EmbeddedBinderConfig{
				MaxBindRetries:        3,
				BindTimeout:           30 * time.Second,
				MaxLocalRetries:       -1,
				QueueBacklogThreshold: 100,
			},
			wantErr: true,
			errMsg:  "MaxLocalRetries must be >= 0",
		},
		{
			name: "MaxLocalRetries zero is valid (disables re-dispatch)",
			config: &EmbeddedBinderConfig{
				MaxBindRetries:        3,
				BindTimeout:           30 * time.Second,
				MaxLocalRetries:       0,
				QueueBacklogThreshold: 100,
			},
			wantErr: false,
		},
		{
			name: "negative QueueBacklogThreshold returns error",
			config: &EmbeddedBinderConfig{
				MaxBindRetries:        3,
				BindTimeout:           30 * time.Second,
				MaxLocalRetries:       5,
				QueueBacklogThreshold: -1,
			},
			wantErr: true,
			errMsg:  "QueueBacklogThreshold must be >= 0",
		},
		{
			name: "MaxBindRetries zero is valid (no retries)",
			config: &EmbeddedBinderConfig{
				MaxBindRetries:        0,
				BindTimeout:           1 * time.Second,
				MaxLocalRetries:       0,
				QueueBacklogThreshold: 0,
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
