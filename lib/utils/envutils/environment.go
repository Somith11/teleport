// Copyright 2021 Gravitational, Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package envutils

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/gravitational/teleport"
	"github.com/gravitational/teleport/lib/utils"
)

// ReadEnvironmentFile will read environment variables from a passed in location.
// Lines that start with "#" or empty lines are ignored. Assignments are in the
// form name=value and no variable expansion occurs.
func ReadEnvironmentFile(filename string) ([]string, error) {
	// open the users environment file. if we don't find a file, move on as
	// having this file for the user is optional.
	file, err := utils.OpenFileNoUnsafeLinks(filename)
	if err != nil {
		log.Warnf("Unable to open environment file %v: %v, skipping", filename, err)
		return []string{}, nil
	}
	defer file.Close()

	var lineno int
	env := &SafeEnv{}

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// follow the lead of OpenSSH and don't allow more than 1,000 environment variables
		// https://github.com/openssh/openssh-portable/blob/master/session.c#L873-L874
		lineno = lineno + 1
		if lineno > teleport.MaxEnvironmentFileLines {
			log.Warnf("Too many lines in environment file %v, returning first %v lines", filename, teleport.MaxEnvironmentFileLines)
			return *env, nil
		}

		// empty lines or lines that start with # are ignored
		if line == "" || line[0] == '#' {
			continue
		}

		// split on first =, if not found, log it and continue
		idx := strings.Index(line, "=")
		if idx == -1 {
			log.Debugf("Bad line %v while reading %v: no = separator found", lineno, filename)
			continue
		}

		// split key and value and make sure that key has a name
		key := line[:idx]
		value := line[idx+1:]
		if strings.TrimSpace(key) == "" {
			log.Debugf("Bad line %v while reading %v: key without name", lineno, filename)
			continue
		}

		// key is added trusted within this context, but should be "AddFullUnique" when combined with any other values
		env.AddTrusted(key, value)
	}

	err = scanner.Err()
	if err != nil {
		log.Warnf("Unable to read environment file %v: %v, skipping", filename, err)
		return []string{}, nil
	}

	return *env, nil
}

var unsafeEnvironmentVars = map[string]struct{}{
	// Linux
	"LD_ASSUME_KERNEL":         {},
	"LD_AUDIT":                 {},
	"LD_BIND_NOW":              {},
	"LD_BIND_NOT":              {},
	"LD_DYNAMIC_WEAK":          {},
	"LD_LIBRARY_PATH":          {},
	"LD_ORIGIN_PATH":           {},
	"LD_POINTER_GUARD":         {},
	"LD_PREFER_MAP_32BIT_EXEC": {},
	"LD_PRELOAD":               {},
	"LD_PROFILE":               {},
	"LD_RUNPATH":               {},
	"LD_RPATH":                 {},
	"LD_USE_LOAD_BIAS":         {},
	// macOS
	"DYLD_INSERT_LIBRARIES": {},
	"DYLD_LIBRARY_PATH":     {},
}

// SafeEnv allows you to build a system environment while avoiding potentially dangerous environment conditions.  In
// addition, SafeEnv will ignore any values added if the key already exists.  This allows earlier inserts to take
// priority and ensure there is no conflicting values.
type SafeEnv []string

// AddTrusted will add the key and value to the environment if it's a safe value to forward on for fork / exec.  This
// will not check for duplicates.
func (e *SafeEnv) AddTrusted(k, v string) {
	e.add(false, k, v)
}

// AddUnique will add the key and value to the environment if it's a safe value to forward on for fork / exec.  If the
// key already exists (case-insensitive) it will be ignored.
func (e *SafeEnv) AddUnique(k, v string) {
	e.add(true, k, v)
}

func (e *SafeEnv) add(preventDuplicates bool, k, v string) {
	k = strings.TrimSpace(k)
	v = strings.TrimSpace(v)
	if e.unsafeKey(preventDuplicates, k) {
		return
	}

	*e = append(*e, fmt.Sprintf("%s=%s", k, v))
}

// AddFullTrusted adds an exact value, in the KEY=VALUE format. This should only be used if they values are already
// combined.  When the values are separate the [Add] function is generally preferred.  This will not check for
// duplicates.
func (e *SafeEnv) AddFullTrusted(fullValues ...string) {
	e.addFull(false, fullValues)
}

// AddFullUnique adds an exact value, in the KEY=VALUE format. This should only be used if they values are already
// combined.  When the values are separate the [Add] function is generally preferred.  If any keys already exists
// (case-insensitive) they will be ignored.
func (e *SafeEnv) AddFullUnique(fullValues ...string) {
	e.addFull(true, fullValues)
}

func (e *SafeEnv) addFull(preventDuplicates bool, fullValues []string) {
	for _, kv := range fullValues {
		kv = strings.TrimSpace(kv)

		key := strings.SplitN(kv, "=", 2)[0]
		if e.unsafeKey(preventDuplicates, key) {
			continue
		}

		*e = append(*e, kv)
	}
}

func (e *SafeEnv) unsafeKey(preventDuplicates bool, key string) bool {
	if key == "" || key == "=" {
		return false
	}

	upperKey := strings.ToUpper(key)
	if _, unsafe := unsafeEnvironmentVars[upperKey]; unsafe {
		return true
	}

	if preventDuplicates {
		prefix := upperKey + "="
		for _, kv := range *e {
			if strings.HasPrefix(strings.ToUpper(kv), prefix) {
				return true
			}
		}
	}

	return false
}

// AddExecEnvironment will add safe values from [os.Environ], ignoring any duplicates that may have already been added.
func (e *SafeEnv) AddExecEnvironment() {
	e.addFull(true, os.Environ())
}
