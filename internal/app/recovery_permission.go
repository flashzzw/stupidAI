package app

import "codex-netcap/internal/runtime"

func invalidatePermissionEvidence(session *Session, view runtime.View) {
	if view.PermissionEvidence != nil {
		policy := queryCopy(*view.PermissionEvidence)
		policy.Verified = false
		session.View.PermissionEvidence = &policy
	} else if session.View.PermissionEvidence != nil {
		// Retain the previous fields and timestamp as historical evidence, but
		// never leave a failed current check labelled as verified.
		session.View.PermissionEvidence.Verified = false
	}
}
