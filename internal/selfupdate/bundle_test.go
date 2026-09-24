package selfupdate

import (
	"encoding/base64"
	"encoding/hex"
	"testing"

	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	protocommon "github.com/sigstore/protobuf-specs/gen/pb-go/common/v1"
	rekorpb "github.com/sigstore/protobuf-specs/gen/pb-go/rekor/v1"
	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/testing/ca"
	"github.com/sigstore/sigstore-go/pkg/tlog"
	"google.golang.org/protobuf/encoding/protojson"
)

// signBundle signs payload as identity/issuer with the virtual CA and
// returns a Sigstore bundle in the JSON form cosign's --bundle writes.
//
// The virtual CA hands back the pieces rather than a bundle, and its
// log entry keeps the signed entry timestamp private, so the timestamp
// is re-signed here over the same payload. With no inclusion proof the
// bundle can only be v0.1, the version that accepts a promise alone.
func signBundle(
	t *testing.T, vs *ca.VirtualSigstore, identity, issuer string,
	payload []byte,
) []byte {
	t.Helper()

	entity, err := vs.Sign(identity, issuer, payload)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	vc, err := entity.VerificationContent()
	if err != nil {
		t.Fatalf("verification content: %v", err)
	}

	sc, err := entity.SignatureContent()
	if err != nil {
		t.Fatalf("signature content: %v", err)
	}

	entries, err := entity.TlogEntries()
	if err != nil {
		t.Fatalf("tlog entries: %v", err)
	}

	tles := make([]*rekorpb.TransparencyLogEntry, 0, len(entries))
	for _, entry := range entries {
		tles = append(tles, promisedEntry(t, vs, entry))
	}

	ms := sc.(*bundle.MessageSignature)

	mediaType, err := bundle.MediaTypeString("v0.1")
	if err != nil {
		t.Fatalf("media type: %v", err)
	}

	pb := &protobundle.Bundle{
		MediaType: mediaType,
		VerificationMaterial: &protobundle.VerificationMaterial{
			Content: &protobundle.VerificationMaterial_X509CertificateChain{
				X509CertificateChain: &protocommon.X509CertificateChain{
					Certificates: []*protocommon.X509Certificate{
						{RawBytes: vc.Certificate().Raw},
					},
				},
			},
			TlogEntries: tles,
		},
		Content: &protobundle.Bundle_MessageSignature{
			MessageSignature: &protocommon.MessageSignature{
				MessageDigest: &protocommon.HashOutput{
					Algorithm: protocommon.HashAlgorithm_SHA2_256,
					Digest:    ms.Digest(),
				},
				Signature: ms.Signature(),
			},
		},
	}

	raw, err := protojson.Marshal(pb)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}

	return raw
}

func promisedEntry(
	t *testing.T, vs *ca.VirtualSigstore, entry *tlog.Entry,
) *rekorpb.TransparencyLogEntry {
	t.Helper()

	tle := entry.TransparencyLogEntry()
	logID := hex.EncodeToString(tle.GetLogId().GetKeyId())

	set, err := vs.RekorSignPayload(tlog.RekorPayload{
		Body:           base64.StdEncoding.EncodeToString(tle.GetCanonicalizedBody()),
		IntegratedTime: tle.GetIntegratedTime(),
		LogIndex:       tle.GetLogIndex(),
		LogID:          logID,
	})
	if err != nil {
		t.Fatalf("sign entry timestamp: %v", err)
	}

	return &rekorpb.TransparencyLogEntry{
		LogIndex:          tle.GetLogIndex(),
		LogId:             tle.GetLogId(),
		KindVersion:       &rekorpb.KindVersion{Kind: "hashedrekord", Version: "0.0.1"},
		IntegratedTime:    tle.GetIntegratedTime(),
		InclusionPromise:  &rekorpb.InclusionPromise{SignedEntryTimestamp: set},
		CanonicalizedBody: tle.GetCanonicalizedBody(),
	}
}
