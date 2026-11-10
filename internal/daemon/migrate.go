package daemon

import (
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// migrateComputers turns what earlier builds of linked computers kept into
// direct links (ADR-0013), once:
//   - f925107 on: signed links and unlinks (memlink, memunlink) and records
//     (computer). A link of this computer's that no unlink names is a
//     direct link (however many earlier ones were unlinked: it's newer);
//     with none, an unlink of the other end refuses its key here.
//   - f191de3 to 443bf28: records (computer), unlinked ones kept as
//     tombstones. One not introduced (no via) is a direct link; a tombstone
//     refuses its key here.
//
// Computers linked only through others are dropped, and named in a note to
// link again (they come back through the others' introductions if those
// are still linked with them). The old records, the cached state of those
// dropped, and the old settings go. Callers hold d.mu.
func (d *Daemon) migrateComputers() {
	old, _ := d.store.Records("computer")
	links, _ := d.store.Records("memlink")
	unlinks, _ := d.store.Records("memunlink")
	dial, _ := d.store.Records("computerdial")
	if len(old)+len(links)+len(unlinks)+len(dial) == 0 && d.store.Setting("computers.out") == "" {
		return
	}
	me := d.id.me.UserId
	recs := map[string]*pb.LegacyComputer{}
	for uid, b := range old {
		r := &pb.LegacyComputer{}
		// (A record whose key isn't its own computer's is skipped.)
		if proto.Unmarshal(b, r) == nil && uid != me && (len(r.Key) == 0 || userIDFor(r.Key) == uid) {
			r.UserId = uid
			recs[uid] = r
		}
	}
	reached := map[string]string{}
	for uid, b := range dial {
		if r := (&pb.Computer{}); proto.Unmarshal(b, r) == nil && len(r.Addresses) == 1 {
			reached[uid] = r.Addresses[0]
		}
	}
	direct, refuse := map[string][]byte{}, map[string]bool{} // direct: its key (if known)
	if len(links) > 0 {
		dead := map[string]map[string]bool{} // link id → computers it's dead for
		for _, b := range unlinks {
			u := &pb.LegacyUnlink{}
			if proto.Unmarshal(b, u) != nil {
				continue
			}
			for _, id := range u.LinkIds {
				if dead[id] == nil {
					dead[id] = map[string]bool{}
				}
				dead[id][u.UserId] = true
			}
		}
		for _, b := range links {
			l := &pb.LegacyLink{}
			if proto.Unmarshal(b, l) != nil {
				continue
			}
			a, bb := userIDFor(l.AKey), userIDFor(l.BKey)
			other, key := bb, l.BKey
			switch me {
			case a:
			case bb:
				other, key = a, l.AKey
			default:
				continue // between two others: not this one's
			}
			switch {
			case dead[l.Id][other]:
				refuse[other] = true
			case dead[l.Id][me]:
				// this one left (or was unlinked): not linked
			default:
				direct[other] = key
			}
		}
		// A live link (one no unlink names) is newer than any dead one: a
		// person linked them again since. It decides.
		for uid := range direct {
			delete(refuse, uid)
		}
	} else {
		for uid, r := range recs {
			switch {
			case r.Unlinked && !r.LocalStone:
				refuse[uid] = true
			case r.Unlinked, r.Via != "":
				// dropped here, or introduced: not a direct link
			default:
				direct[uid] = r.Key
			}
		}
	}
	var again []string
	for uid, r := range recs {
		key, ok := direct[uid]
		switch {
		case ok && r.CertSha256 != "" && len(r.Addresses) > 0:
			if d.comp.links[uid] != nil {
				continue
			}
			rec := &pb.Computer{UserId: uid, Name: r.Name, Model: r.Model, Kind: r.Kind, Os: r.Os, Tint: r.Tint,
				Fingerprint: keyFingerprint(uid), LinkedUnixMs: r.LinkedUnixMs, Addresses: r.Addresses, CertSha256: r.CertSha256, Key: cmpBytes(key, r.Key)}
			if !tintRE.MatchString(rec.Tint) {
				rec.Tint = ""
			}
			sl := &pb.StoredLink{Record: rec, CertSha256: r.CertSha256, ReachedAt: reached[uid]}
			d.persist(d.store.PutRecord("complink", uid, sl))
		case refuse[uid]:
			d.persist(d.store.PutRecord("compdeny", uid, &pb.Refusal{UserId: uid, Name: r.Name, UnixMs: time.Now().UnixMilli()}))
			d.persist(d.store.DeleteRecord("peercache", r.CertSha256))
		default:
			if !r.Unlinked {
				again = append(again, cmpOr(r.Name, keyFingerprint(uid)))
			}
			d.persist(d.store.DeleteRecord("peercache", r.CertSha256))
		}
	}
	for uid := range refuse {
		if recs[uid] == nil {
			d.persist(d.store.PutRecord("compdeny", uid, &pb.Refusal{UserId: uid, UnixMs: time.Now().UnixMilli()}))
		}
	}
	for uid := range old {
		d.persist(d.store.DeleteRecord("computer", uid))
	}
	for _, kind := range []string{"memlink", "memunlink", "computerdial"} {
		recs, _ := d.store.Records(kind)
		for id := range recs {
			d.persist(d.store.DeleteRecord(kind, id))
		}
	}
	d.persist(d.store.SetSetting("computers.out", ""))
	if len(again) > 0 {
		slices.Sort(again)
		note := "Linked with an earlier build, through your other computers: " + strings.Join(again, ", ") +
			". If they don't come back on their own, link them again."
		d.persist(d.store.SetSetting("computers.note", note))
	}
	d.audit("computer", "migrated", "", "linked computers kept as direct links")
}
