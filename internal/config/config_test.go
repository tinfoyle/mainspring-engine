package config

import "testing"

func TestFirecrawlProviderRequiresAnEndpoint(t *testing.T) {
	t.Setenv("MAINSPRING_MCP_TOKEN", "")
	t.Setenv("MAINSPRING_WEB_RESEARCH_PROVIDER", "firecrawl")
	t.Setenv("MAINSPRING_FIRECRAWL_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("Firecrawl provider without an endpoint should fail configuration")
	}

	t.Setenv("MAINSPRING_FIRECRAWL_URL", "http://firecrawl-api:3002")
	config, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if config.FirecrawlURL != "http://firecrawl-api:3002" || config.FirecrawlTimeout <= 0 {
		t.Fatalf("unexpected Firecrawl configuration: %#v", config)
	}
}
