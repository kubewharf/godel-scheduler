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

package tracing

import (
	"errors"

	"github.com/spf13/pflag"
	utilpointer "k8s.io/utils/pointer"
)

var UnSupportedTracer error = errors.New("unsupported tracer")

type TracerConfiguration struct {
	// IDCName specifies the name of idc to deploy godel dispatcher
	IDCName *string

	// ClusterName specifies the name of cluster to serve
	ClusterName *string

	// Tracer defines to enable tracing or not
	Tracer *string
}

func DefaultNoopOptions() *TracerConfiguration {
	return &TracerConfiguration{
		Tracer:      utilpointer.StringPtr(string(NoopConfig)),
		ClusterName: utilpointer.StringPtr(Cluster),
		IDCName:     utilpointer.StringPtr(IDC),
	}
}

func (opt *TracerConfiguration) Validate() error {
	if opt == nil {
		return nil
	}

	if opt.Tracer == nil {
		return nil
	}

	switch *opt.Tracer {
	case string(NoopConfig):
		return nil
	default:
		return UnSupportedTracer
	}
}

func (opt *TracerConfiguration) AddFlags(fs *pflag.FlagSet) {
	if opt == nil {
		return
	}

	fs.StringVar(opt.IDCName, "trace-idc", *opt.IDCName, "the idc name of deployment.")
	fs.StringVar(opt.ClusterName, "trace-cluster", *opt.ClusterName, "the cluster name of deployment.")
	fs.StringVar(opt.Tracer, "tracer", *opt.Tracer, "tracer to use, options are jaeger and noop.")
}

func (opt *TracerConfiguration) ApplyTo(options *TracerConfiguration) {
	if opt == nil {
		return
	}

	if opt.IDCName != nil {
		options.IDCName = opt.IDCName
	}

	if opt.ClusterName != nil {
		options.ClusterName = opt.ClusterName
	}

	if opt.Tracer != nil {
		options.Tracer = opt.Tracer
	}
}

func (opt *TracerConfiguration) DeepCopyInto(out *TracerConfiguration) {
	if opt == nil {
		return
	}

	if opt.IDCName != nil {
		out.IDCName = utilpointer.StringPtr(*opt.IDCName)
	}

	if opt.ClusterName != nil {
		out.ClusterName = utilpointer.StringPtr(*opt.ClusterName)
	}

	if opt.Tracer != nil {
		out.Tracer = utilpointer.StringPtr(*opt.Tracer)
	}
}

func (opt *TracerConfiguration) DeepCopy() (out *TracerConfiguration) {
	if opt == nil {
		return nil
	}
	out = &TracerConfiguration{}

	if opt.IDCName != nil {
		out.IDCName = utilpointer.StringPtr(*opt.IDCName)
	}

	if opt.ClusterName != nil {
		out.ClusterName = utilpointer.StringPtr(*opt.ClusterName)
	}

	if opt.Tracer != nil {
		out.Tracer = utilpointer.StringPtr(*opt.Tracer)
	}
	return out
}
