package pricing

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// UserPriceTable is an optional, user-supplied price catalog that takes
// precedence over every online and embedded source. It is deliberately small:
// per-region, per-instance-type hourly prices plus explicit discount rules.
//
// The parser never lets a duplicate declaration win silently. Every duplicate
// key is reported with the line and column of both the first and the repeated
// occurrence.
type UserPriceTable struct {
	Defaults DiscountRules
	Families map[string]DiscountRules
	Regions  map[string]map[string]UserPriceEntry
}

// DiscountRules holds optional, already-validated discounts in the range
// [0, 1). A nil pointer means "not configured at this level" so that a more
// specific rule can be selected instead of multiplying rules together.
//
// OnDemand and Spot are independent: on-demand discounts never apply to spot
// prices, and an enterprise discount is never composed with the spot fallback
// multiplier or with Spot Advisor savings.
type DiscountRules struct {
	OnDemand *float64
	Spot     *float64
}

// UserPriceEntry is one instance type's hourly prices in USD.
// OnDemand is required; Spot is optional. When Spot is absent a spot request
// misses the user table and continues down the normal provider chain.
type UserPriceEntry struct {
	OnDemand float64
	Spot     *float64
}

// LoadUserPriceTable reads and validates a user price table from path.
func LoadUserPriceTable(path string) (*UserPriceTable, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	t, err := ParseUserPriceTable(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return t, nil
}

// ParseUserPriceTable parses and validates a user price table.
func ParseUserPriceTable(data []byte) (*UserPriceTable, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return &UserPriceTable{Families: map[string]DiscountRules{}, Regions: map[string]map[string]UserPriceEntry{}}, nil
	}

	root := doc.Content[0]
	if root.Kind == yaml.ScalarNode && root.Tag == "!!null" {
		return &UserPriceTable{Families: map[string]DiscountRules{}, Regions: map[string]map[string]UserPriceEntry{}}, nil
	}
	if root.Kind != yaml.MappingNode {
		return nil, at(root, "price table must be a mapping")
	}

	t := &UserPriceTable{
		Families: map[string]DiscountRules{},
		Regions:  map[string]map[string]UserPriceEntry{},
	}

	seen := map[string]*yaml.Node{}
	for i := 0; i < len(root.Content); i += 2 {
		key, val := root.Content[i], root.Content[i+1]
		if prev, ok := seen[key.Value]; ok {
			return nil, duplicateKeyError("top-level key", key, prev)
		}
		seen[key.Value] = key

		switch key.Value {
		case "defaults":
			rules, err := parseDiscountRules(val)
			if err != nil {
				return nil, err
			}
			t.Defaults = rules
		case "families":
			fams, err := parseFamilies(val)
			if err != nil {
				return nil, err
			}
			t.Families = fams
		case "regions":
			regions, err := parseRegions(val)
			if err != nil {
				return nil, err
			}
			t.Regions = regions
		default:
			return nil, at(key, "unknown top-level key %q (expected defaults, families or regions)", key.Value)
		}
	}

	return t, nil
}

func parseFamilies(n *yaml.Node) (map[string]DiscountRules, error) {
	fams := map[string]DiscountRules{}
	if isNull(n) {
		return fams, nil
	}
	if n.Kind != yaml.MappingNode {
		return nil, at(n, "families must be a mapping of family name to discount rules")
	}
	seen := map[string]*yaml.Node{}
	for i := 0; i < len(n.Content); i += 2 {
		key, val := n.Content[i], n.Content[i+1]
		if key.Value == "" {
			return nil, at(key, "instance family name must not be empty")
		}
		if prev, ok := seen[key.Value]; ok {
			return nil, duplicateKeyError("instance family", key, prev)
		}
		seen[key.Value] = key

		rules, err := parseDiscountRules(val)
		if err != nil {
			return nil, err
		}
		if rules.OnDemand == nil && rules.Spot == nil {
			return nil, at(key, "instance family %q declares no discount", key.Value)
		}
		fams[key.Value] = rules
	}
	return fams, nil
}

func parseRegions(n *yaml.Node) (map[string]map[string]UserPriceEntry, error) {
	regions := map[string]map[string]UserPriceEntry{}
	if isNull(n) {
		return regions, nil
	}
	if n.Kind != yaml.MappingNode {
		return nil, at(n, "regions must be a mapping of region name to instance prices")
	}
	seenRegions := map[string]*yaml.Node{}
	for i := 0; i < len(n.Content); i += 2 {
		regionKey, regionVal := n.Content[i], n.Content[i+1]
		if regionKey.Value == "" {
			return nil, at(regionKey, "region name must not be empty")
		}
		if prev, ok := seenRegions[regionKey.Value]; ok {
			return nil, duplicateKeyError("region", regionKey, prev)
		}
		seenRegions[regionKey.Value] = regionKey

		if regionVal.Kind != yaml.MappingNode {
			return nil, at(regionVal, "region %q must be a mapping of instance type to price", regionKey.Value)
		}

		instances := map[string]UserPriceEntry{}
		seenTypes := map[string]*yaml.Node{}
		for j := 0; j < len(regionVal.Content); j += 2 {
			typeKey, typeVal := regionVal.Content[j], regionVal.Content[j+1]
			if typeKey.Value == "" {
				return nil, at(typeKey, "instance type must not be empty")
			}
			if prev, ok := seenTypes[typeKey.Value]; ok {
				return nil, duplicateKeyError("instance type", typeKey, prev)
			}
			seenTypes[typeKey.Value] = typeKey

			entry, err := parseUserPriceEntry(typeVal)
			if err != nil {
				return nil, err
			}
			instances[typeKey.Value] = entry
		}
		regions[regionKey.Value] = instances
	}
	return regions, nil
}

func parseUserPriceEntry(n *yaml.Node) (UserPriceEntry, error) {
	var entry UserPriceEntry
	if n.Kind != yaml.MappingNode {
		return entry, at(n, "instance entry must be a mapping with on_demand (and optional spot)")
	}

	seen := map[string]*yaml.Node{}
	hasOnDemand := false
	for i := 0; i < len(n.Content); i += 2 {
		key, val := n.Content[i], n.Content[i+1]
		if prev, ok := seen[key.Value]; ok {
			return entry, duplicateKeyError("price field", key, prev)
		}
		seen[key.Value] = key

		price, err := parsePrice(val, key.Value)
		if err != nil {
			return entry, err
		}
		switch key.Value {
		case "on_demand":
			entry.OnDemand = price
			hasOnDemand = true
		case "spot":
			p := price
			entry.Spot = &p
		default:
			return entry, at(key, "unknown price field %q (expected on_demand or spot)", key.Value)
		}
	}

	if !hasOnDemand {
		return entry, at(n, "instance entry is missing required on_demand price")
	}
	return entry, nil
}

func parseDiscountRules(n *yaml.Node) (DiscountRules, error) {
	var rules DiscountRules
	if isNull(n) {
		return rules, nil
	}
	if n.Kind != yaml.MappingNode {
		return rules, at(n, "discount rules must be a mapping")
	}
	seen := map[string]*yaml.Node{}
	for i := 0; i < len(n.Content); i += 2 {
		key, val := n.Content[i], n.Content[i+1]
		if prev, ok := seen[key.Value]; ok {
			return rules, duplicateKeyError("discount field", key, prev)
		}
		seen[key.Value] = key

		switch key.Value {
		case "on_demand_discount", "spot_discount":
			v, err := parseFloatNode(val)
			if err != nil {
				return rules, err
			}
			if v < 0 || v >= 1 {
				return rules, at(val, "%s must be in [0, 1), got %v", key.Value, v)
			}
			if key.Value == "on_demand_discount" {
				rules.OnDemand = &v
			} else {
				rules.Spot = &v
			}
		default:
			return rules, at(key, "unknown discount field %q (expected on_demand_discount or spot_discount)", key.Value)
		}
	}
	return rules, nil
}

func parsePrice(n *yaml.Node, field string) (float64, error) {
	v, err := parseFloatNode(n)
	if err != nil {
		return 0, err
	}
	if v < 0 {
		return 0, at(n, "%s price must not be negative, got %v", field, v)
	}
	return v, nil
}

func parseFloatNode(n *yaml.Node) (float64, error) {
	if n.Kind != yaml.ScalarNode || (n.Tag != "!!float" && n.Tag != "!!int") {
		return 0, at(n, "expected a number, got %s %q", n.Tag, n.Value)
	}
	var v float64
	if err := n.Decode(&v); err != nil {
		return 0, at(n, "invalid number %q: %v", n.Value, err)
	}
	return v, nil
}

func isNull(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.Tag == "!!null"
}

func at(n *yaml.Node, format string, args ...any) error {
	return fmt.Errorf("line %d:%d: %s", n.Line, n.Column, fmt.Sprintf(format, args...))
}

func duplicateKeyError(kind string, key, prev *yaml.Node) error {
	return fmt.Errorf("line %d:%d: duplicate %s %q (first declared at line %d:%d)",
		key.Line, key.Column, kind, key.Value, prev.Line, prev.Column)
}

// lookup returns the user price for an instance, applying at most one discount.
// It reports false when the table has no applicable entry for the request.
func (t *UserPriceTable) lookup(cloud, region, instanceType string, isSpot bool) (float64, bool) {
	if t == nil {
		return 0, false
	}
	if !isKnownCloud(cloud) {
		return 0, false
	}
	instances, ok := t.Regions[region]
	if !ok {
		return 0, false
	}
	entry, ok := instances[instanceType]
	if !ok {
		return 0, false
	}

	if isSpot {
		if entry.Spot == nil {
			// No spot price declared: fall through to the normal chain rather
			// than deriving one (which would stack an on-demand discount onto a
			// spot estimate).
			return 0, false
		}
		disc := t.spotDiscountFor(instanceType)
		return *entry.Spot * (1 - disc), true
	}

	disc := t.onDemandDiscountFor(instanceType)
	return entry.OnDemand * (1 - disc), true
}

// onDemandDiscountFor selects the most specific explicit on-demand discount:
// instance family first, then global. It never multiplies rules together.
func (t *UserPriceTable) onDemandDiscountFor(instanceType string) float64 {
	if rules, ok := t.Families[familyOf(instanceType)]; ok && rules.OnDemand != nil {
		return *rules.OnDemand
	}
	if t.Defaults.OnDemand != nil {
		return *t.Defaults.OnDemand
	}
	return 0
}

func (t *UserPriceTable) spotDiscountFor(instanceType string) float64 {
	if rules, ok := t.Families[familyOf(instanceType)]; ok && rules.Spot != nil {
		return *rules.Spot
	}
	if t.Defaults.Spot != nil {
		return *t.Defaults.Spot
	}
	return 0
}

// familyOf returns the instance family used for family discount lookup. For
// dotted names (AWS/GCP) that is the part before the first dot; otherwise the
// full name is its own family.
func familyOf(instanceType string) string {
	if i := strings.IndexByte(instanceType, '.'); i > 0 {
		return instanceType[:i]
	}
	return instanceType
}
