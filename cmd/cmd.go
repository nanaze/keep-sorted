// Copyright 2023 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/google/keep-sorted/keepsorted"
	flag "github.com/spf13/pflag"
	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"
)

type Config struct {
	id            string
	operation     operation
	modifiedLines []keepsorted.LineRange
}

func (c *Config) FromFlags(fs *flag.FlagSet) {
	if fs == nil {
		fs = flag.CommandLine
	}

	fs.StringVar(&c.id, "id", "keep-sorted", "The identifier used to enable this tool in files.")
	fs.MarkHidden("id")

	of := &operationFlag{op: &c.operation}
	if err := of.Set("fix"); err != nil {
		panic(err)
	}
	fs.Var(of, "mode", fmt.Sprintf("Determines what mode to run this tool in. One of %q", knownModes()))

	fs.Var(&lineRangeFlag{lineRanges: &c.modifiedLines}, "lines", "Line ranges of the form \"start:end\". Only processes keep-sorted blocks that overlap with the given line ranges. Can only be used when fixing a single file.")
}

var (
	operations = map[string]operation{
		"lint": lint,
		"fix":  fix,
	}
)

func knownModes() []string {
	ms := maps.Keys(operations)
	slices.Sort(ms)
	return ms
}

type operation func(id string, filenames []string, modifiedLines []keepsorted.LineRange) (ok bool, err error)

type operationFlag struct {
	op *operation
	s  string
}

func (f *operationFlag) String() string {
	return f.s
}

func (f *operationFlag) Set(val string) error {
	op := operations[val]
	if op == nil {
		return fmt.Errorf("unknown mode %q. Valid modes: %q", val, knownModes())
	}
	*f.op = op
	return nil
}

func (f *operationFlag) Type() string {
	return "mode"
}

type lineRangeFlag struct {
	lineRanges *[]keepsorted.LineRange
	changed    bool
	s          []string
}

func (f *lineRangeFlag) String() string {
	return "[" + strings.Join(f.GetSlice(), ",") + "]"
}

func (f *lineRangeFlag) Set(val string) error {
	vals := strings.Split(val, ",")
	if !f.changed {
		return f.Replace(vals)
	}

	for _, val := range vals {
		if err := f.Append(val); err != nil {
			return err
		}
	}
	return nil
}

func (f *lineRangeFlag) Type() string {
	return "line ranges"
}

func (f *lineRangeFlag) Append(val string) error {
	f.changed = true
	lrs, err := f.parse([]string{val})
	if err != nil {
		return err
	}
	*f.lineRanges = append(*f.lineRanges, lrs...)
	f.s = append(f.s, val)
	return nil
}

func (f *lineRangeFlag) Replace(vals []string) error {
	f.changed = true
	lrs, err := f.parse(vals)
	if err != nil {
		return err
	}
	*f.lineRanges = lrs
	f.s = vals
	return nil
}

func (f *lineRangeFlag) parse(vals []string) ([]keepsorted.LineRange, error) {
	var lrs []keepsorted.LineRange
	for _, val := range vals {
		sp := strings.SplitN(val, ":", 2)
		start, err := strconv.Atoi(sp[0])
		if err != nil {
			return nil, fmt.Errorf("invalid line range %q: %w", val, err)
		}
		end := -1
		if len(sp) == 1 {
			end = start
		} else {
			end, err = strconv.Atoi(sp[1])
			if err != nil {
				return nil, fmt.Errorf("invalid line range %q: %w", val, err)
			}
		}

		lrs = append(lrs, keepsorted.LineRange{start, end})
	}
	return lrs, nil
}

func (f *lineRangeFlag) GetSlice() []string {
	return f.s
}

const (
	stdin = "-"
)

func Run(c *Config, files []string) (ok bool, err error) {
	if c.id == "" {
		return false, errors.New("id cannot be empty")
	}

	if len(files) == 0 {
		return false, errors.New("must pass one or more filenames")
	}

	if len(c.modifiedLines) > 0 && len(files) > 1 {
		return false, errors.New("cannot specify modifiedLines with more than one file")
	}

	return c.operation(c.id, files, c.modifiedLines)
}

func fix(id string, filenames []string, modifiedLines []keepsorted.LineRange) (ok bool, err error) {
	for _, fn := range filenames {
		contents, err := read(fn)
		if err != nil {
			return false, err
		}
		if want, alreadyFixed := keepsorted.New(id).Fix(contents, modifiedLines); fn == stdin || !alreadyFixed {
			if err := write(fn, want); err != nil {
				return false, err
			}
		}
	}
	return true, nil
}

func lint(id string, filenames []string, modifiedLines []keepsorted.LineRange) (ok bool, err error) {
	var fs []*keepsorted.Finding
	for _, fn := range filenames {
		contents, err := read(fn)
		if err != nil {
			return false, err
		}
		fs = append(fs, keepsorted.New(id).Findings(fn, contents, modifiedLines)...)
	}

	if len(fs) == 0 {
		return true, nil
	}

	out := json.NewEncoder(os.Stdout)
	out.SetIndent("", "  ")
	if err := out.Encode(fs); err != nil {
		return false, fmt.Errorf("could not write findings to stdout: %w", err)
	}

	return false, nil
}

func read(fn string) (string, error) {
	if fn == stdin {
		b, err := io.ReadAll(os.Stdin)
		return string(b), err
	}

	b, err := os.ReadFile(fn)
	return string(b), err
}

func write(fn string, s string) error {
	if fn == stdin {
		_, err := os.Stdout.WriteString(s)
		return err
	}

	return os.WriteFile(fn, []byte(s), 0644)
}
