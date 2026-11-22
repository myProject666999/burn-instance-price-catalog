package pricing

// Canonical cost constants shared by the pricing and billing packages.
//
// HoursPerMonth is the conventional AWS/GCP/Azure projection month
// (365 * 24 / 12 = 730). It is deliberately not a real calendar month; callers
// that need calendar months must compute them separately.
const (
	HoursPerMonth          = 730.0
	DaysPerMonth           = HoursPerMonth / 24.0
	SpotFallbackMultiplier = 0.21
)

// defaultRegions are the region whose catalog entry is reused, and marked as a
// fallback, when no data exists for the requested region.
var defaultRegions = map[string]string{
	"aws":   "us-east-1",
	"azure": "eastus",
	"gcp":   "us-central1",
}

func defaultRegionFor(cloud string) (string, bool) {
	r, ok := defaultRegions[normalizeCloud(cloud)]
	return r, ok
}

func normalizeCloud(cloud string) string {
	switch cloud {
	case "AWS", "Aws":
		return "aws"
	case "AZURE", "Azure":
		return "azure"
	case "GCP", "Gcp":
		return "gcp"
	default:
		return cloud
	}
}

func isKnownCloud(cloud string) bool {
	_, ok := defaultRegions[normalizeCloud(cloud)]
	return ok
}
