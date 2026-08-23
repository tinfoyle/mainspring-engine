package marketing

import "testing"

func TestAssetObjectIdentityIsDerivedAndVersionReferenceIsCanonical(t *testing.T) {
	key, err := AssetObjectKey("d1100000-0000-4000-8000-000000000001", "d1200000-0000-4000-8000-000000000002",
		"d1300000-0000-4000-8000-000000000003", "d1400000-0000-4000-8000-000000000004")
	if err != nil || key != "accounts/d1100000-0000-4000-8000-000000000001/marketing/campaigns/d1200000-0000-4000-8000-000000000002/assets/d1300000-0000-4000-8000-000000000003/revisions/d1400000-0000-4000-8000-000000000004/content" {
		t.Fatalf("key=%q err=%v", key, err)
	}
	reference, err := ContentReferenceForObjectVersion("opaque/version+1=")
	if err != nil {
		t.Fatal(err)
	}
	version, err := ObjectVersionFromContentReference(reference)
	if err != nil || version != "opaque/version+1=" {
		t.Fatalf("reference=%q version=%q err=%v", reference, version, err)
	}
	for _, invalid := range []string{"", " arbitrary", reference + "=", "object-version-v1.IA"} {
		if _, err := ObjectVersionFromContentReference(invalid); err == nil {
			t.Fatalf("invalid reference accepted: %q", invalid)
		}
	}
}
