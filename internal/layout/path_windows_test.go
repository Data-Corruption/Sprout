package layout

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsDirectoryOwnerPolicy(t *testing.T) {
	user, err := windows.StringToSid("S-1-5-21-1-2-3-1001")
	if err != nil {
		t.Fatal(err)
	}
	otherUser, err := windows.StringToSid("S-1-5-21-1-2-3-1002")
	if err != nil {
		t.Fatal(err)
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name                string
		owner, defaultOwner *windows.SID
		want                bool
	}{
		{"user-owned", user, user, true},
		{"elevated-default-owner", admins, admins, true},
		{"user-owned-under-elevation", user, admins, true},
		{"admins-not-this-tokens-owner", admins, user, false},
		{"foreign-user", otherUser, admins, false},
		{"missing-owner", nil, admins, false},
		{"missing-token-owner", user, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ownerMatchesToken(tc.owner, user, tc.defaultOwner); got != tc.want {
				t.Fatalf("accepted=%t, want %t", got, tc.want)
			}
		})
	}
}

func TestWindowsAcceptsCreatedAndExplicitUserOwnedDirectories(t *testing.T) {
	root := t.TempDir()
	// This directory is Administrators-owned on the elevated GitHub runner.
	if err := validatePrivateDir(root); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "explicit-user")
	if err := os.Mkdir(p, 0o700); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, user.User.Sid, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := validatePrivateDir(p); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validatePrivateDir(file); err == nil {
		t.Fatal("accepted a file as a directory")
	}
}
