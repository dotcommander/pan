package main

import "testing"

func TestParseOptions(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		args    []string
		wantErr string
		want    options
	}{
		{
			name: "version only normalizes",
			args: []string{"v0.2.0"},
			want: options{version: "0.2.0"},
		},
		{
			name: "publish",
			args: []string{"0.2.0", "--publish"},
			want: options{version: "0.2.0", publish: true},
		},
		{
			name:    "resume requires publish",
			args:    []string{"0.2.0", "--resume"},
			wantErr: "--resume requires --publish",
		},
		{
			name:    "adopt requires publish",
			args:    []string{"0.2.0", "--adopt"},
			wantErr: "--adopt requires --publish",
		},
		{
			name: "adopt publish",
			args: []string{"0.2.0", "--publish", "--adopt"},
			want: options{version: "0.2.0", publish: true, adopt: true},
		},
		{
			name: "adopt resume publish",
			args: []string{"0.2.0", "--publish", "--resume", "--adopt"},
			want: options{version: "0.2.0", publish: true, resume: true, adopt: true},
		},
		{
			name:    "unknown option",
			args:    []string{"0.2.0", "--wat"},
			wantErr: `unknown option "--wat"`,
		},
		{
			name:    "missing version",
			args:    []string{},
			wantErr: "version is required",
		},
		{
			name:    "multiple versions",
			args:    []string{"0.2.0", "0.3.0"},
			wantErr: `multiple versions supplied: "0.2.0" and "0.3.0"`,
		},
		{
			name: "help",
			args: []string{"--help"},
			want: options{help: true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseOptions(tc.args)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("parseOptions(%v) error = %v, want %q", tc.args, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseOptions(%v) error: %v", tc.args, err)
			}
			if got != tc.want {
				t.Errorf("parseOptions(%v) = %+v, want %+v", tc.args, got, tc.want)
			}
		})
	}
}
