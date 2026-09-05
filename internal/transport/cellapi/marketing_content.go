package cellapi

import (
	"mime"
	"net/http"
	"path/filepath"
	"strconv"

	marketingapp "github.com/tinfoyle/spyglass-engine/internal/application/marketing"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

func (s *Server) marketingAssetContent(w http.ResponseWriter, r *http.Request) {
	claims, actor, accountID, ok := s.marketingReadRequest(w, r)
	if !ok {
		return
	}
	campaignID, ok := marketingCampaignTarget(w, r)
	if !ok {
		return
	}
	if s.marketingContent == nil {
		s.writeMarketingError(w, "download asset", marketingapp.ErrRepository)
		return
	}
	if len(r.URL.Query()) != 0 {
		s.writeMarketingError(w, "download asset", marketingapp.ErrInvalid)
		return
	}
	asset, body, err := s.marketingContent.Download(routecontext.WithClaims(r.Context(), claims), actor, accountID, campaignID, ids.MarketingAssetRevisionID(r.PathValue("revisionID")))
	if err != nil {
		s.writeMarketingError(w, "download asset", err)
		return
	}
	filename := filepath.Base(asset.Title)
	if filepath.Ext(filename) == "" {
		if extensions, _ := mime.ExtensionsByType(asset.MediaType); len(extensions) > 0 {
			filename += extensions[0]
		}
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
