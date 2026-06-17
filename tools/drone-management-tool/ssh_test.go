package main

import "testing"

func TestNormalizeSSHParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		req     SSHConnectRequest
		want    SSHConnectRequest
		wantErr bool
	}{
		{
			name: "default port",
			req:  SSHConnectRequest{Host: "192.168.1.10", User: "root"},
			want: SSHConnectRequest{Host: "192.168.1.10", Port: 22, User: "root"},
		},
		{
			name: "host includes port",
			req:  SSHConnectRequest{Host: "192.168.1.10:2222", Port: 22, User: "root"},
			want: SSHConnectRequest{Host: "192.168.1.10", Port: 2222, User: "root"},
		},
		{
			name:    "missing host",
			req:     SSHConnectRequest{User: "root"},
			wantErr: true,
		},
		{
			name:    "invalid port",
			req:     SSHConnectRequest{Host: "192.168.1.10", Port: 70000, User: "root"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := normalizeSSHParams(tt.req)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("normalizeSSHParams() expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeSSHParams() unexpected error: %v", err)
			}
			if got.Host != tt.want.Host || got.Port != tt.want.Port || got.User != tt.want.User {
				t.Fatalf("normalizeSSHParams() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
