package daemon

import (
	"encoding/json"
	"errors"
	"slices"

	"google.golang.org/protobuf/proto"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Your sidebar's order: your own Groups and those shared with you, mixed,
// as you arrange them. Your Groups keep their position in their Order; a
// shared Group's position is kept here (its host's own ordering doesn't
// move it in your sidebar).

const remoteOrderKey = "order.shared"

// loadRemoteOrder reads where you've put Groups shared with you. Callers hold d.mu.
func (d *Daemon) loadRemoteOrder() {
	d.remoteOrder = map[string]int32{}
	_ = json.Unmarshal([]byte(d.store.Setting(remoteOrderKey)), &d.remoteOrder)
}

func (d *Daemon) saveRemoteOrder() {
	b, _ := json.Marshal(d.remoteOrder)
	d.persist(d.store.SetSetting(remoteOrderKey, string(b)))
}

// nextOrder is a position after every Group you have. Callers hold d.mu.
func (d *Daemon) nextOrder() int32 {
	order := int32(0)
	for _, g := range d.groups {
		order = max(order, g.Order+1)
	}
	for _, l := range d.links {
		for _, g := range l.groups {
			order = max(order, g.Order+1)
		}
	}
	return order
}

// sharedOrder is where a Group shared with you goes: where you put it, else
// after everything. Callers hold d.mu.
func (d *Daemon) sharedOrder(id string) int32 {
	if o, ok := d.remoteOrder[id]; ok {
		return o
	}
	o := d.nextOrder()
	d.remoteOrder[id] = o
	d.saveRemoteOrder()
	return o
}

// moveGroup moves a Group to index among all of yours. Callers hold d.mu.
func (d *Daemon) moveGroup(id string, index int) error {
	type entry struct {
		g      *pb.Group
		shared bool
	}
	var all []entry
	for _, g := range d.groups {
		all = append(all, entry{g, false})
	}
	for _, l := range d.links {
		for _, g := range l.groups {
			all = append(all, entry{g, true})
		}
	}
	slices.SortStableFunc(all, func(a, b entry) int {
		if a.g.Order != b.g.Order {
			return int(a.g.Order - b.g.Order)
		}
		return compareStrings(a.g.Id, b.g.Id)
	})
	from := slices.IndexFunc(all, func(e entry) bool { return e.g.Id == id })
	if from < 0 {
		return errors.New("no such group")
	}
	e := all[from]
	all = slices.Delete(all, from, from+1)
	index = max(0, min(index, len(all)))
	all = slices.Insert(all, index, e)
	u := &pb.StateUpdate{}
	for i, e := range all {
		if e.g.Order == int32(i) {
			continue
		}
		e.g.Order = int32(i)
		if e.shared {
			d.remoteOrder[e.g.Id] = int32(i)
		} else {
			d.persist(d.store.PutGroup(e.g))
		}
		u.Groups = append(u.Groups, proto.CloneOf(e.g))
	}
	d.saveRemoteOrder()
	if len(u.Groups) > 0 {
		d.publish(u)
	}
	return nil
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
