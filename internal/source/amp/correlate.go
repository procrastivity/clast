package amp

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/procrastivity/clast/internal/source"
)

// Correlate binds a thread to the working directory it was created in —
// env.initial inside its export. The discriminator per the contract is
// the local install id: only a thread whose platform.installationID
// equals this machine's device-id.json gets its workingDirectory (else
// trees[0].uri) decoded to a local path. Absent env (virtual executors),
// foreign executor types (sandbox/virtual), other installs, and a
// present hostname that contradicts the install match all return "" —
// projectless but capturable; a foreign path is never guessed into a
// local clone, and repo.url/projectID stay artifact-side provenance.
//
// The env.initial evidence is creation-frozen: a continued-elsewhere
// thread keeps its birth environment, which is exactly what the install
// match verifies.
//
// Verdicts are also recorded in the scope checkpoint keyed on the
// enumerated revision — a second look at a revision already answered on
// this host (an unchanged projectless commit, a sweep that re-emits it)
// returns the recorded verdict instead of paying another full export
// just to re-answer the same question.
func (s *Source) Correlate(ctx context.Context, d source.Discovered) (string, []source.Diagnostic, error) {
	local := s.localInstallID()
	if local == "" {
		// No local install at all → every thread is foreign; the
		// verdict needs no fetch.
		return "", nil, nil
	}
	mine, herr := s.host()
	if herr == nil {
		if v, ok := s.cachedVerdict(d.NativeID, revisionKey(d), mine); ok {
			return v.Dir, nil, nil
		}
	}
	// A hostname that cannot be read skips the cache entirely — the
	// veto below consults it, and a verdict recorded under unknown host
	// would be unkeyed evidence.
	_, doc, err := s.exportFor(ctx, d)
	if err != nil {
		return "", nil, err
	}
	dir := correlateVerdict(doc, local, mine, herr)
	if herr == nil {
		s.recordVerdict(d.NativeID, revisionKey(d), mine, dir)
	}
	return dir, nil, nil
}

// correlateVerdict is the doc-evidence half of Correlate, split out so
// the live verdict and the recorded verdict are the same computation.
// mine/hostErr carry the pre-read hostname: a matching install id is
// still declined when the export's hostname contradicts this machine —
// shared-install setups (synced dotfiles) must never map a remote path
// onto a local clone — while an absent or unreadable hostname declines
// nothing: the install id is the discriminator (step-02 §4).
func correlateVerdict(doc *ExportDoc, local, mine string, hostErr error) string {
	init := doc.Env.Initial
	if init == nil {
		return "" // virtual executors carry no env at all
	}
	switch doc.Meta.ExecutorType {
	case "virtual", "sandbox":
		return ""
	}
	if init.Platform == nil || init.Platform.InstallationID != local {
		return "" // born under another install
	}
	if host := init.Hostname; host != "" && hostErr == nil && host != mine {
		return ""
	}
	uri := init.WorkingDirectory
	if uri == "" && len(init.Trees) > 0 {
		uri = init.Trees[0].URI
	}
	return fileURIToPath(uri)
}

// revisionKey renders the enumerated revision a verdict keys on — the
// canonical form of Discovered.ModTime, so the same instant spelled
// with a different zone still hits the entry.
func revisionKey(d source.Discovered) string {
	return d.ModTime.UTC().Format(time.RFC3339Nano)
}

// fileURIToPath decodes a file:// URI to a local path. Anything else —
// a foreign host, a non-file scheme, a malformed or relative URI — is ""
// rather than a guessed path.
func fileURIToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	if u.Host != "" && u.Host != "localhost" {
		return ""
	}
	if !strings.HasPrefix(u.Path, "/") {
		return ""
	}
	return u.Path
}
