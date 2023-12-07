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

package version

import (
	"fmt"
	"runtime"
	"strings"
)

// Info contains versioning information.
type Info struct {
	Major        string `json:"major"`
	Minor        string `json:"minor"`
	GitVersion   string `json:"gitVersion"`
	GitRemote    string `json:"gitRemote"`
	GitCommit    string `json:"gitCommit"`
	GitTreeState string `json:"gitTreeState"`
	BuildDate    string `json:"buildDate"`
	GoVersion    string `json:"goVersion"`
	Compiler     string `json:"compiler"`
	Platform     string `json:"platform"`
}

// Get returns the overall codebase version. It's for detecting
// what code a binary was built from.
func Get() Info {
	majorVersion, minorVersion := splitVersion(gitVersion)
	return Info{
		Major:        majorVersion,
		Minor:        minorVersion,
		GitVersion:   gitVersion,
		GitCommit:    gitCommit,
		GitRemote:    gitRemote,
		BuildDate:    buildDate,
		GitTreeState: gitTreeState,
		GoVersion:    runtime.Version(),
		Compiler:     runtime.Compiler,
		Platform:     fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}
}

// String returns info as a human-friendly version string.
func (info Info) String() string {
	return info.GitVersion
}

// splitVersion splits the git version to generate major and minor versions needed.
func splitVersion(version string) (major, minor string) {
	// remove leading v if present
	if strings.HasSuffix(version, "v") {
		version = strings.TrimLeft(version, "v")
	}

	if dot := strings.Index(version, "."); dot > 0 {
		major = version[:dot]
		minor = version[dot+1:]
	}
	return
}
