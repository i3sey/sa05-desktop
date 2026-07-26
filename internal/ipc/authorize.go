package ipc

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
)

// firstNormalUID is the conventional start of human accounts on Linux. Service accounts
// below it have no business asking for a tunnel.
const firstNormalUID = 1000

// AllowUIDs authorizes only the listed uids, plus root. This is the production policy:
// the installer records the uid that owns the desktop session.
func AllowUIDs(uids ...uint32) Authorizer {
	allowed := map[uint32]bool{0: true}
	for _, uid := range uids {
		allowed[uid] = true
	}
	return func(credentials PeerCredentials) error {
		if allowed[credentials.UID] {
			return nil
		}
		return fmt.Errorf("uid %d не имеет доступа к системному компоненту", credentials.UID)
	}
}

// AllowLoginUsers authorizes root and any regular login account. It is the default when
// the helper is installed without a fixed uid, e.g. on a multi-user desktop.
func AllowLoginUsers() Authorizer {
	return func(credentials PeerCredentials) error {
		if credentials.UID == 0 || credentials.UID >= firstNormalUID {
			return nil
		}
		return fmt.Errorf("uid %d не имеет доступа к системному компоненту", credentials.UID)
	}
}

// ParseUIDs converts user names or numeric ids into uids for AllowUIDs.
func ParseUIDs(values []string) ([]uint32, error) {
	uids := make([]uint32, 0, len(values))
	for _, value := range values {
		if numeric, err := strconv.ParseUint(value, 10, 32); err == nil {
			uids = append(uids, uint32(numeric))
			continue
		}
		account, err := user.Lookup(value)
		if err != nil {
			return nil, fmt.Errorf("пользователь %q не найден: %w", value, err)
		}
		numeric, err := strconv.ParseUint(account.Uid, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("некорректный uid пользователя %q: %w", value, err)
		}
		uids = append(uids, uint32(numeric))
	}
	return uids, nil
}

// CurrentUID is the uid this process runs as, used when the helper is started by hand for
// debugging.
func CurrentUID() uint32 { return uint32(os.Getuid()) }
