package runner

import "io"

// FakeCall records one invocation made through a hooked Runner. Tests
// inspect Calls after the plugin under test runs to assert that exactly the
// expected argv was produced.
type FakeCall struct {
	Name string
	Args []string
	Sudo bool
	User string
}

// Fake is a Runner whose Hook records each call into Calls and consults
// Respond for scripted output. Use NewFake to construct one.
type Fake struct {
	*Runner
	Calls   []FakeCall
	Respond func(name string, args []string) (stdout, stderr string, exitCode int, err error)
}

// NewFake returns a hooked Runner that records every call and returns
// scripted responses. With no Respond set it returns empty stdout, no
// stderr, exit code 0, and nil error.
func NewFake() *Fake {
	f := &Fake{
		Runner: &Runner{
			DryRun: false,
			Stdout: io.Discard,
			Stderr: io.Discard,
		},
	}
	f.Runner.Hook = func(spec Spec) (string, string, int, error) {
		f.Calls = append(f.Calls, FakeCall{
			Name: spec.Name,
			Args: append([]string(nil), spec.Args...),
			Sudo: spec.Sudo,
			User: spec.User,
		})
		if f.Respond != nil {
			return f.Respond(spec.Name, spec.Args)
		}
		return "", "", 0, nil
	}
	return f
}
