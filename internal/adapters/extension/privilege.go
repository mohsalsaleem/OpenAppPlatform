package extension

import (
	"os"
	"syscall"
)

// Extensions run as nobody when oapd runs as root (R5.8): they can't read
// oapd's data (its journal, desired state, secret store and its key), only
// what they are granted through Configure. An extension granted the
// docker_socket setting also gets that socket's group: Docker access is
// root on the host, so it is only ever an explicit grant.
const nobody = 65534

func unprivileged(cfg Config) *syscall.SysProcAttr {
	if os.Geteuid() != 0 {
		return nil // already unprivileged, as in tests
	}
	cred := &syscall.Credential{Uid: nobody, Gid: nobody, Groups: []uint32{}}
	if sock, ok := cfg.Settings["docker_socket"]; ok {
		if st, err := os.Stat(sock); err == nil {
			if sys, ok := st.Sys().(*syscall.Stat_t); ok {
				cred.Groups = append(cred.Groups, sys.Gid)
			}
		}
	}
	return &syscall.SysProcAttr{Credential: cred}
}
