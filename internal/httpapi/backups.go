package httpapi

import (
	"github.com/mohsalsaleem/OpenAppPlatform/internal/backupreports"
	"net/http"
	"os"
)

func (s *Server) backupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/access/backups", func(w http.ResponseWriter, r *http.Request) {
		overview, err := backupreports.Read(os.Getenv("OAP_BACKUP_REPORTS_DIR"))
		if err != nil {
			write(w, 503, map[string]string{"message": "Backup reports are unavailable. Check the configured read-only report mount."})
			return
		}
		write(w, 200, overview)
	})
}
