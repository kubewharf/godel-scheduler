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

package route

import (
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"
	"path"
	"sync"

	"k8s.io/apiserver/pkg/server/mux"
	"k8s.io/klog/v2"
)

var (
	lock            = &sync.RWMutex{}
	registeredFlags = map[string]debugFlag{}
)

// DebugFlags adds handlers for flags under /debug/flags.
type DebugFlags struct{}

// Install registers the APIServer's flags handler.
func (f DebugFlags) Install(c *mux.PathRecorderMux, flag string, handler func(http.ResponseWriter, *http.Request)) {
	c.UnlistedHandle("/debug/flags", http.HandlerFunc(f.Index))
	c.UnlistedHandlePrefix("/debug/flags/", http.HandlerFunc(f.Index))

	url := path.Join("/debug/flags", flag)
	c.UnlistedHandleFunc(url, handler)

	f.addFlag(flag)
}

// Index responds with the `/debug/flags` request.
// For example, "/debug/flags/v" serves the "--v" flag.
// Index responds to a request for "/debug/flags/" with an HTML page
// listing the available flags.
func (f DebugFlags) Index(w http.ResponseWriter, r *http.Request) {
	lock.RLock()
	defer lock.RUnlock()
	if err := indexTmpl.Execute(w, registeredFlags); err != nil {
		klog.InfoS("Index failed", "err", err)
	}
}

var indexTmpl = template.Must(template.New("index").Parse(`<html>
<head>
<title>/debug/flags/</title>
</head>
<body>
/debug/flags/<br>
<br>
flags:<br>
<table>
{{range .}}
<tr>{{.Flag}}<br>
{{end}}
</table>
<br>
full flags configurable<br>
</body>
</html>
`))

type debugFlag struct {
	Flag string
}

func (f DebugFlags) addFlag(flag string) {
	lock.Lock()
	defer lock.Unlock()
	registeredFlags[flag] = debugFlag{flag}
}

// StringFlagSetterFunc is a func used for setting string type flag.
type StringFlagSetterFunc func(string) (string, error)

// StringFlagGetterFunc is a func used for getting string type flag.
type StringFlagGetterFunc func() (string, error)

// StringFlagHandler wraps an http Handler to set string type flag.
func StringFlagHandler(setter StringFlagSetterFunc, getter StringFlagGetterFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == "PUT":
			body, err := ioutil.ReadAll(req.Body)
			if err != nil {
				writePlainText(http.StatusBadRequest, "error reading request body: "+err.Error(), w)
				return
			}
			defer req.Body.Close()
			response, err := setter(string(body))
			if err != nil {
				writePlainText(http.StatusBadRequest, err.Error(), w)
				return
			}
			writePlainText(http.StatusOK, response, w)

		case req.Method == "GET":
			response, err := getter()
			if err != nil {
				writePlainText(http.StatusBadRequest, fmt.Sprintf("failed to get log verbosity, err: %v", err), w)
				return
			}
			writePlainText(http.StatusOK, response, w)

		default:
			writePlainText(http.StatusNotAcceptable, "unsupported http method", w)
		}
	})
}

// writePlainText renders a simple string response.
func writePlainText(statusCode int, text string, w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(statusCode)
	fmt.Fprintln(w, text)
}

// GlogSetter is a setter to set glog level.
func GlogSetter(val string) (string, error) {
	var level klog.Level
	if err := level.Set(val); err != nil {
		return "", fmt.Errorf("failed set klog.logging.verbosity %s: %v", val, err)
	}
	return fmt.Sprintf("successfully set klog.logging.verbosity to %s", val), nil
}

// GlogGetter is used to get glog level.
func GlogGetter() (string, error) {
	vflag := flag.Lookup("v")
	if vflag == nil {
		return "", fmt.Errorf("unknow flag v")
	}

	getter, ok := vflag.Value.(flag.Getter)
	if !ok {
		return "", fmt.Errorf("unsupport get function")
	}

	l, ok := getter.Get().(klog.Level)
	if !ok {
		return "", fmt.Errorf("expect klog.Level, get %T", getter.Get())
	}
	return fmt.Sprintf("klog.logging.verbosity is %s", l.String()), nil
}
