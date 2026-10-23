package service

import (
	"testing"

	"ci-platform/internal/model"
)

func TestValidateSettingsSshServers(t *testing.T) {
	ok := model.Settings{SshServers: []model.SshServer{
		{Name: "prod", Host: "deploy.example.com", Username: "deploy", AuthType: "password", Password: "p"},
	}}
	if err := validateSettings(&ok); err != nil {
		t.Fatalf("valid server rejected: %v", err)
	}
	if ok.SshServers[0].ID == "" {
		t.Fatal("new server should get an id")
	}
	if ok.SshServers[0].Port != 22 {
		t.Fatalf("port = %d, want 22", ok.SshServers[0].Port)
	}
	if ok.SshServers[0].HasPassword {
		t.Fatal("has_password must not be persisted")
	}

	bad := []model.SshServer{
		{Name: "", Host: "h", Username: "u", AuthType: "password", Password: "p"},
		{Name: "x", Host: "", Username: "u", AuthType: "password", Password: "p"},
		{Name: "x", Host: "h", Username: "", AuthType: "password", Password: "p"},
		{Name: "x", Host: "h", Username: "u", AuthType: "password"},
		{Name: "x", Host: "h", Username: "u", AuthType: "key"},
		{Name: "x", Host: "h", Username: "u", AuthType: "bogus"},
	}
	for _, server := range bad {
		in := model.Settings{SshServers: []model.SshServer{server}}
		if err := validateSettings(&in); err == nil {
			t.Errorf("server %+v should be rejected", server)
		}
	}
}
