package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

// A one-way stamp detects an environment value changing without a revision
// update. The raw value stays in the runtime's credential handoff only.
func CredentialStamp(route Route) (string, error) {
	key, ok := strings.CutPrefix(route.CredentialRef, "env:")
	if !ok {
		return "", nil
	}
	value := os.Getenv(key)
	if key == "" || value == "" {
		return "", fmt.Errorf("路由凭据环境变量未设置")
	}
	return credentialStamp(route.CredentialRef, value), nil
}

func credentialStamp(reference, value string) string {
	sum := sha256.Sum256([]byte("netcap-route-credential-v1\x00" + reference + "\x00" + value))
	return hex.EncodeToString(sum[:])
}

func VerifyCredentialStamp(scope Scope) error {
	current, err := CredentialStamp(scope.Route)
	if err != nil {
		return err
	}
	if current != "" && (scope.CredentialStamp == "" || scope.CredentialStamp != current) {
		return fmt.Errorf("会话凭据快照缺失或已变化，请更新 revision 后创建新会话")
	}
	return nil
}
