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
	"fmt"
	"time"
)

const (
	// DefaultMaxBindRetries is the default maximum number of times a single Pod
	// bind operation is retried inside the Binder before giving up.
	DefaultMaxBindRetries = 3

	// DefaultBindTimeout is the default timeout for a single bind operation.
	DefaultBindTimeout = 30 * time.Second

	// DefaultMaxLocalRetries is the default cumulative failure count (across both
	// Scheduler and Binder) after which a Pod is dispatched back to the Dispatcher
	// rather than retried locally.
	DefaultMaxLocalRetries = 5

	// DefaultQueueBacklogThreshold is the default threshold for the Binder's
	// internal work queue. Exceeding this triggers alerting / back-pressure.
	DefaultQueueBacklogThreshold = 100
)

// EmbeddedBinderConfig holds tuning parameters for the embedded (per-Scheduler)
// Binder. All fields have sensible defaults provided by DefaultEmbeddedBinderConfig().
type EmbeddedBinderConfig struct {
	// MaxBindRetries is the maximum number of times a single Pod bind API call
	// is retried on transient errors (e.g. 409 Conflict) before considering
	// the bind a failure.
	MaxBindRetries int

	// BindTimeout is the maximum wall-clock duration for binding all Pods in a
	// single BindRequest. Zero is invalid.
	BindTimeout time.Duration

	// MaxLocalRetries is the cumulative number of scheduling + binding failures
	// a Pod may experience within the same Scheduler before the Binder requests
	// a re-dispatch to the Dispatcher. A value of 0 disables the re-dispatch
	// mechanism (local retries are unlimited).
	MaxLocalRetries int

	// QueueBacklogThreshold is the length at which the Binder's internal
	// work queue is considered overloaded. Used for metrics and alerting only;
	// it does not enforce back-pressure.
	QueueBacklogThreshold int
}

// DefaultEmbeddedBinderConfig returns an EmbeddedBinderConfig with production-
// ready default values.
func DefaultEmbeddedBinderConfig() *EmbeddedBinderConfig {
	return &EmbeddedBinderConfig{
		MaxBindRetries:        DefaultMaxBindRetries,
		BindTimeout:           DefaultBindTimeout,
		MaxLocalRetries:       DefaultMaxLocalRetries,
		QueueBacklogThreshold: DefaultQueueBacklogThreshold,
	}
}

// Validate checks that the configuration is semantically correct.
func (c *EmbeddedBinderConfig) Validate() error {
	if c == nil {
		return fmt.Errorf("EmbeddedBinderConfig must not be nil")
	}
	if c.MaxBindRetries < 0 {
		return fmt.Errorf("MaxBindRetries must be >= 0, got %d", c.MaxBindRetries)
	}
	if c.BindTimeout <= 0 {
		return fmt.Errorf("BindTimeout must be > 0, got %v", c.BindTimeout)
	}
	if c.MaxLocalRetries < 0 {
		return fmt.Errorf("MaxLocalRetries must be >= 0, got %d", c.MaxLocalRetries)
	}
	if c.QueueBacklogThreshold < 0 {
		return fmt.Errorf("QueueBacklogThreshold must be >= 0, got %d", c.QueueBacklogThreshold)
	}
	return nil
}
