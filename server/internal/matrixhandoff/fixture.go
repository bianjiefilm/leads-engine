package matrixhandoff

// Fixture is the in-process matrix. It does not dial, publish, or charge.
// Published and Outcome may repeat a dishonest success claim; Service ignores them.
type Fixture struct {
	Err       error
	Accept    bool
	Published bool
	Outcome   string
	Calls     int
	Payloads  []Payload
}

// Ack is the fixture reply. Accepted means the reference was taken.
// Published and Outcome are not a live matrix receipt.
type Ack struct {
	Accepted  bool
	Published bool
	Outcome   string
}

// Client submits one restricted payload.
type Client interface {
	SubmitDraft(Payload) (Ack, error)
}

// Payload is the only document the fixture may see.
type Payload struct {
	SourceIDs SourceIDs `json:"source_ids"`
	AssetID   string    `json:"asset_id"`
	Version   int       `json:"version"`
	ReturnRef string    `json:"return_ref"`
}

// NewFixture accepts drafts until Err is set.
func NewFixture() *Fixture {
	return &Fixture{Accept: true}
}

// SubmitDraft records a payload only when the fixture does not fail.
func (f *Fixture) SubmitDraft(p Payload) (Ack, error) {
	if f == nil {
		return Ack{}, ErrClient
	}
	f.Calls++
	if f.Err != nil {
		return Ack{}, f.Err
	}
	f.Payloads = append(f.Payloads, p)
	return Ack{Accepted: f.Accept, Published: f.Published, Outcome: f.Outcome}, nil
}
