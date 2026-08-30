package webui

import (
	"math"
	"math/rand"
)

// mockParticipants returns a deterministic allowlist of enrolled research
// participants. This stands in for what would, in a real deployment, come from a
// vetted enrollment/consent system. Contacts are pre-masked; no raw phone
// numbers exist in this data.
func mockParticipants() []*Participant {
	return []*Participant{
		{
			ID: "p-001", Label: "Lab device A (Pixel 7)", MaskedContact: "+1 415 ••• ••11",
			ConsentStatus: ConsentVerified, ConsentRef: "IRB-2026-014 / form C-3",
			OwnershipVerified: true, ConsentExpiry: "2027-01-01", Enrolled: true,
		},
		{
			ID: "p-002", Label: "Lab device B (iPhone 14)", MaskedContact: "+1 415 ••• ••22",
			ConsentStatus: ConsentVerified, ConsentRef: "IRB-2026-014 / form C-4",
			OwnershipVerified: true, ConsentExpiry: "2027-01-01", Enrolled: true,
		},
		{
			ID: "p-003", Label: "Volunteer V. Okafor", MaskedContact: "+44 20 •••• ••33",
			ConsentStatus: ConsentVerified, ConsentRef: "consent-2026-088",
			OwnershipVerified: false, ConsentExpiry: "2026-12-31", Enrolled: true,
		},
		{
			ID: "p-004", Label: "Volunteer M. Reyes (consent pending)", MaskedContact: "+1 206 ••• ••44",
			ConsentStatus: ConsentPending, ConsentRef: "", OwnershipVerified: false,
			ConsentExpiry: "", Enrolled: true,
		},
		{
			ID: "p-005", Label: "Volunteer T. Nawaz (consent expired)", MaskedContact: "+92 21 •••• ••55",
			ConsentStatus: ConsentExpired, ConsentRef: "consent-2025-201",
			OwnershipVerified: false, ConsentExpiry: "2026-01-15", Enrolled: true,
		},
		{
			ID: "p-006", Label: "Former volunteer (consent revoked)", MaskedContact: "+1 312 ••• ••66",
			ConsentStatus: ConsentRevoked, ConsentRef: "consent-2025-140",
			OwnershipVerified: false, ConsentExpiry: "2027-01-01", Enrolled: false,
		},
	}
}

// mockConfounders returns the standard set of factors that can distort RTT.
func mockConfounders() []Confounder {
	return []Confounder{
		{Name: "Network RTT", Impact: "high", Note: "Cellular/Wi-Fi latency adds directly to measured RTT and varies over time."},
		{Name: "Packet loss", Impact: "medium", Note: "Retransmits inflate a subset of samples, widening the tail."},
		{Name: "Server load", Impact: "medium", Note: "Fan-out and queueing on the messaging backend add variable delay."},
		{Name: "Connection reuse", Impact: "low", Note: "A warm connection is faster than a cold one; first probes may look slower."},
	}
}

// mockQRMatrix returns a small deterministic boolean matrix used only to draw a
// placeholder "QR" on screen. It intentionally encodes nothing: it is not a
// pairing code and carries no credential. Real QR contents are never produced
// or returned by this package.
func mockQRMatrix(seed int64) [][]bool {
	const n = 21
	r := rand.New(rand.NewSource(seed))
	m := make([][]bool, n)
	for i := range m {
		m[i] = make([]bool, n)
		for j := range m[i] {
			m[i][j] = r.Intn(2) == 1
		}
	}
	// Draw the three finder squares so it reads visually as a QR placeholder.
	drawFinder(m, 0, 0)
	drawFinder(m, 0, n-7)
	drawFinder(m, n-7, 0)
	return m
}

func drawFinder(m [][]bool, top, left int) {
	for i := 0; i < 7; i++ {
		for j := 0; j < 7; j++ {
			edge := i == 0 || i == 6 || j == 0 || j == 6
			inner := i >= 2 && i <= 4 && j >= 2 && j <= 4
			m[top+i][left+j] = edge || inner
		}
	}
}

// sampleGen produces mock RTT samples whose distribution depends on the approved
// test state, so the live chart shows a believable — but clearly synthetic —
// signal. Each state centres the RTT in a different hypothesis band while adding
// jitter and the occasional failure, illustrating real-world overlap.
type sampleGen struct {
	r         *rand.Rand
	testState string
}

func newSampleGen(testState string, seed int64) *sampleGen {
	return &sampleGen{r: rand.New(rand.NewSource(seed)), testState: testState}
}

func (g *sampleGen) next(tMillis int64) Sample {
	// ~4% of probes fail/timeout, illustrating packet loss / unreachable states.
	if g.r.Float64() < 0.04 {
		return Sample{T: tMillis, RTTMs: 0, BandKey: "doze", Success: false}
	}
	var center, spread float64
	switch g.testState {
	case "foreground_baseline":
		center, spread = 180, 90
	case "background_baseline":
		center, spread = 600, 220
	case "screen_off_baseline":
		center, spread = 1900, 700
	case "network_control":
		center, spread = 350, 250
	default:
		center, spread = 700, 400
	}
	rtt := center + g.r.NormFloat64()*spread
	if rtt < 40 {
		rtt = 40 + math.Abs(g.r.NormFloat64())*20 // Floor: physical network minimum.
	}
	rtt = math.Round(rtt)
	return Sample{T: tMillis, RTTMs: rtt, BandKey: BandFor(rtt), Success: true}
}
