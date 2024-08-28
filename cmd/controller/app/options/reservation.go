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
	"github.com/spf13/pflag"

	"github.com/kubewharf/godel-scheduler/pkg/controller/reservation/config"
)

type ReservationControllerOptions struct {
	*config.ReservationControllerConfiguration
}

func (opt *ReservationControllerOptions) AddFlags(fs *pflag.FlagSet) {
	if opt == nil {
		return
	}
	fs.Int64Var(&opt.ReservationTTL, "reservation-ttl", opt.ReservationTTL, "how long resources will be reserved (for resource reservation).")
	fs.Int64Var(&opt.MatchedRequestExtraTTL, "matched-request-cleanup-ttl", opt.MatchedRequestExtraTTL, "how long matched requests will be recycled after reservation ttl")
	fs.StringSliceVar(&opt.IgnoredNamespace, "ignored-namespace-list", opt.IgnoredNamespace, "The list of namespace to be ignored when setup informer.")
}

func (opt *ReservationControllerOptions) ApplyTo(cfg *config.ReservationControllerConfiguration) error {
	if opt == nil {
		return nil
	}
	cfg.ReservationTTL = opt.ReservationTTL
	cfg.ReservationCheckPeriod = opt.ReservationCheckPeriod
	cfg.MatchedRequestExtraTTL = opt.MatchedRequestExtraTTL
	cfg.IgnoredNamespace = opt.IgnoredNamespace
	return nil
}

// TODO
func (opt *ReservationControllerOptions) Validate() error {
	if opt == nil {
		return nil
	}
	return nil
}
