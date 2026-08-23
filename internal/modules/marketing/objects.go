package marketing

import (
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	contentReferencePrefix = "object-version-v1."
	maximumObjectVersion   = 256
)

func AssetObjectKey(accountID ids.AccountID, campaignID ids.MarketingCampaignID, assetID ids.MarketingAssetID, revisionID ids.MarketingAssetRevisionID) (string, error) {
	if ids.Validate(string(accountID)) != nil || ids.Validate(string(campaignID)) != nil || ids.Validate(string(assetID)) != nil || ids.Validate(string(revisionID)) != nil {
		return "", ErrInvalid
	}
	return fmt.Sprintf("accounts/%s/marketing/campaigns/%s/assets/%s/revisions/%s/content", accountID, campaignID, assetID, revisionID), nil
}

func ContentReferenceForObjectVersion(version string) (string, error) {
	if !validObjectVersion(version) {
		return "", ErrInvalid
	}
	value := contentReferencePrefix + base64.RawURLEncoding.EncodeToString([]byte(version))
	if len(value) > MaximumContentReferenceBytes {
		return "", ErrInvalid
	}
	return value, nil
}

func ObjectVersionFromContentReference(reference string) (string, error) {
	if reference == "" || reference != strings.TrimSpace(reference) || !strings.HasPrefix(reference, contentReferencePrefix) || len(reference) > MaximumContentReferenceBytes {
		return "", ErrInvalid
	}
	encoded := strings.TrimPrefix(reference, contentReferencePrefix)
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || base64.RawURLEncoding.EncodeToString(decoded) != encoded {
		return "", ErrInvalid
	}
	version := string(decoded)
	if !validObjectVersion(version) {
		return "", ErrInvalid
	}
	return version, nil
}

func validObjectVersion(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= maximumObjectVersion && utf8.ValidString(value) &&
		strings.IndexFunc(value, func(current rune) bool { return current < 0x20 || current == 0x7f }) == -1
}
