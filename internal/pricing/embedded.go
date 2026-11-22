package pricing

import (
	"embed"
	"fmt"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed data/*.yaml
var priceData embed.FS

type priceDB struct {
	aws   map[string]map[string]float64
	azure map[string]map[string]float64
	gcp   map[string]map[string]float64
	mu    sync.RWMutex
}

var db *priceDB
var dbOnce sync.Once

func loadPriceDB() *priceDB {
	dbOnce.Do(func() {
		db = &priceDB{
			aws:   make(map[string]map[string]float64),
			azure: make(map[string]map[string]float64),
			gcp:   make(map[string]map[string]float64),
		}
		db.aws = loadYAML("data/aws.yaml")
		db.azure = loadYAML("data/azure.yaml")
		db.gcp = loadYAML("data/gcp.yaml")
	})
	return db
}

func loadYAML(path string) map[string]map[string]float64 {
	data, err := priceData.ReadFile(path)
	if err != nil {
		return nil
	}

	var prices map[string]map[string]float64
	if err := yaml.Unmarshal(data, &prices); err != nil {
		return nil
	}
	return prices
}

// regionPrice looks up a price and reports whether the requested region was
// missing and the default region was used instead.
func regionPrice(prices map[string]map[string]float64, region, instanceType, fallbackRegion string) (price float64, fallback bool, ok bool) {
	if prices == nil {
		return 0, false, false
	}
	regionPrices, exists := prices[region]
	fallback = false
	if !exists {
		regionPrices, exists = prices[fallbackRegion]
		if !exists {
			return 0, false, false
		}
		fallback = true
	}
	price, ok = regionPrices[instanceType]
	return price, fallback, ok
}

func (p *priceDB) getAWSPrice(region, instanceType string) (float64, bool, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return regionPrice(p.aws, region, instanceType, defaultRegions["aws"])
}

func (p *priceDB) getAzurePrice(region, vmSize string) (float64, bool, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return regionPrice(p.azure, region, vmSize, defaultRegions["azure"])
}

func (p *priceDB) getGCPPrice(region, machineType string) (float64, bool, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return regionPrice(p.gcp, region, machineType, defaultRegions["gcp"])
}

// GetEmbeddedPrice returns the price from embedded YAML data.
//
// When the requested region is absent the default region's entry is used; this
// preserves the historical behaviour but the caller cannot tell it happened.
// Prefer LookupEmbeddedPrice when the fallback matters.
func GetEmbeddedPrice(cloud, region, instanceType string) (float64, error) {
	price, _, err := LookupEmbeddedPrice(cloud, region, instanceType)
	return price, err
}

// LookupEmbeddedPrice returns the embedded price and whether the region was
// substituted with the cloud's default region.
func LookupEmbeddedPrice(cloud, region, instanceType string) (float64, bool, error) {
	db := loadPriceDB()
	cloud = normalizeCloud(cloud)

	var price float64
	var fallback, ok bool

	switch cloud {
	case "aws":
		price, fallback, ok = db.getAWSPrice(region, instanceType)
	case "azure":
		price, fallback, ok = db.getAzurePrice(region, instanceType)
	case "gcp":
		price, fallback, ok = db.getGCPPrice(region, instanceType)
	default:
		return 0, false, fmt.Errorf("unknown cloud: %s", cloud)
	}

	if !ok {
		return 0, false, fmt.Errorf("price not found for %s/%s/%s", cloud, region, instanceType)
	}
	return price, fallback, nil
}

// validateEmbeddedData checks the embedded catalogs for duplicate keys and
// negative prices. It is wired into tests via TestEmbeddedCatalogIntegrity and
// can be called at startup for defence in depth.
func validateEmbeddedData() error {
	for _, path := range []string{"data/aws.yaml", "data/azure.yaml", "data/gcp.yaml"} {
		data, err := priceData.ReadFile(path)
		if err != nil {
			return err
		}
		if err := validatePriceYAML(data); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	return nil
}

func validatePriceYAML(data []byte) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	if len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return at(root, "expected a mapping of region to prices")
	}
	seenRegions := map[string]*yaml.Node{}
	for i := 0; i < len(root.Content); i += 2 {
		regionKey, regionVal := root.Content[i], root.Content[i+1]
		if prev, ok := seenRegions[regionKey.Value]; ok {
			return duplicateKeyError("region", regionKey, prev)
		}
		seenRegions[regionKey.Value] = regionKey
		if regionVal.Kind != yaml.MappingNode {
			return at(regionVal, "region %q must map instance types to numbers", regionKey.Value)
		}
		seenTypes := map[string]*yaml.Node{}
		for j := 0; j < len(regionVal.Content); j += 2 {
			typeKey, typeVal := regionVal.Content[j], regionVal.Content[j+1]
			if prev, ok := seenTypes[typeKey.Value]; ok {
				return duplicateKeyError("instance type", typeKey, prev)
			}
			seenTypes[typeKey.Value] = typeKey
			price, err := parseFloatNode(typeVal)
			if err != nil {
				return err
			}
			if price < 0 {
				return at(typeVal, "negative price %v for %s/%s", price, regionKey.Value, typeKey.Value)
			}
		}
	}
	return nil
}
