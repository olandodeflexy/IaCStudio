//go:build windows

package agentrouting

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func securePolicyStoreDir(path string) error {
	return securePolicyStoreObject(path, windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT)
}

func securePolicyStoreFile(path string) error {
	return securePolicyStoreObject(path, 0)
}

func securePolicyStoreObject(path string, inheritance uint32) error {
	currentUser, err := currentPolicyStoreUserSID()
	if err != nil {
		return err
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return fmt.Errorf("resolve administrators SID: %w", err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("resolve local system SID: %w", err)
	}

	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{
		policyStoreAccessEntry(currentUser, windows.TRUSTEE_IS_USER, inheritance),
		policyStoreAccessEntry(administrators, windows.TRUSTEE_IS_GROUP, inheritance),
		policyStoreAccessEntry(system, windows.TRUSTEE_IS_USER, inheritance),
	}, nil)
	if err != nil {
		return fmt.Errorf("build policy store ACL: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	); err != nil {
		return fmt.Errorf("apply policy store ACL: %w", err)
	}
	return nil
}

func policyStoreAccessEntry(sid *windows.SID, trusteeType windows.TRUSTEE_TYPE, inheritance uint32) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.ACCESS_MASK(windows.GENERIC_ALL),
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  trusteeType,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
}

func currentPolicyStoreUserSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("resolve current user SID: %w", err)
	}
	sid, err := user.User.Sid.Copy()
	if err != nil {
		return nil, fmt.Errorf("copy current user SID: %w", err)
	}
	return sid, nil
}
