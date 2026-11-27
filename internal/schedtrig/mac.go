package schedtrig

// This Mac triggers (pb.MacTrigger), written out as mac:WHAT:
//
//	mac:disk<10% | mac:disk<50GB  [@VOLUME: a mount point; none: the startup disk]
//	mac:battery<20%
//	mac:plugged-in | mac:unplugged
//	mac:wake[=MIN]          it wakes, having slept at least MIN minutes
//	mac:away=MIN            you've been away for MIN minutes
//	mac:back[=MIN]          you're back, after at least MIN minutes away (10)
//	mac:drive[=NAME]        an external drive is connected (whose name has NAME)
//	mac:wifi-joined[=NAME] | mac:wifi-left[=NAME]
//	mac:app-opened=NAME | mac:app-quit=NAME   (its name or bundle id)
//	mac:display[=NAME]
//	place:arrive=NAME | place:leave=NAME   (one of the Mac app's Places)

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Defaults, for a This Mac trigger that doesn't say.
const (
	MacDiskPercentDefault    = 10
	MacBatteryPercentDefault = 20
	MacAwayDefault           = 15
	MacBackDefault           = 10
)

var macEvents = map[string]pb.MacTrigger_Event{
	"disk": pb.MacTrigger_EVENT_DISK_LOW, "battery": pb.MacTrigger_EVENT_BATTERY_LOW,
	"plugged-in": pb.MacTrigger_EVENT_PLUGGED_IN, "unplugged": pb.MacTrigger_EVENT_UNPLUGGED,
	"wake": pb.MacTrigger_EVENT_WAKE, "away": pb.MacTrigger_EVENT_AWAY, "back": pb.MacTrigger_EVENT_BACK,
	"drive": pb.MacTrigger_EVENT_DRIVE, "wifi-joined": pb.MacTrigger_EVENT_WIFI_JOINED, "wifi-left": pb.MacTrigger_EVENT_WIFI_LEFT,
	"app-opened": pb.MacTrigger_EVENT_APP_OPENED, "app-quit": pb.MacTrigger_EVENT_APP_QUIT, "display": pb.MacTrigger_EVENT_DISPLAY,
}

// MacEventName is the word mac: reads for e ("plugged-in").
func MacEventName(e pb.MacTrigger_Event) string {
	for k, v := range macEvents {
		if v == e {
			return k
		}
	}
	return ""
}

// MacAppWatched: the Mac app watches for e (the daemon can't), so it fires
// only while the app is open.
func MacAppWatched(e pb.MacTrigger_Event) bool {
	switch e {
	case pb.MacTrigger_EVENT_WIFI_JOINED, pb.MacTrigger_EVENT_WIFI_LEFT, pb.MacTrigger_EVENT_APP_OPENED,
		pb.MacTrigger_EVENT_APP_QUIT, pb.MacTrigger_EVENT_DISPLAY, pb.MacTrigger_EVENT_PLACE_ARRIVE, pb.MacTrigger_EVENT_PLACE_LEAVE:
		return true
	}
	return false
}

// IsPlace: e is arriving at or leaving a place.
func IsPlace(e pb.MacTrigger_Event) bool {
	return e == pb.MacTrigger_EVENT_PLACE_ARRIVE || e == pb.MacTrigger_EVENT_PLACE_LEAVE
}

// MaxPlaceName is the longest a place's name may be.
const MaxPlaceName = 64

// parsePlace reads what follows place: (rest): arrive=NAME or leave=NAME.
func parsePlace(spec, rest string) (*pb.MacTrigger, error) {
	word, name, _ := strings.Cut(strings.TrimSpace(rest), "=")
	name = strings.TrimSpace(name)
	m := &pb.MacTrigger{Name: name}
	switch strings.ToLower(strings.TrimSpace(word)) {
	case "arrive", "arrives", "arrived", "enter", "at":
		m.Event = pb.MacTrigger_EVENT_PLACE_ARRIVE
	case "leave", "leaves", "left", "exit":
		m.Event = pb.MacTrigger_EVENT_PLACE_LEAVE
	default:
		return nil, fmt.Errorf("%q: place:arrive=NAME or place:leave=NAME (one of the Mac app's Places)", spec)
	}
	if name == "" {
		return nil, fmt.Errorf("%q: which place? place:%s=NAME", spec, strings.ToLower(strings.TrimSpace(word)))
	}
	return m, nil
}

const macUsage = "mac:disk<10%|disk<50GB[@VOLUME], battery<20%, plugged-in, unplugged, wake[=MIN], away=MIN, back[=MIN], drive[=NAME], wifi-joined[=NAME], wifi-left[=NAME], app-opened=NAME, app-quit=NAME or display[=NAME]"

// parseMac reads what follows mac: (rest).
func parseMac(spec, rest string) (*pb.MacTrigger, error) {
	rest = strings.TrimSpace(rest)
	word, val := rest, ""
	if i := strings.IndexAny(rest, "=<"); i >= 0 {
		word, val = rest[:i], strings.TrimSpace(rest[i+1:])
	}
	word = strings.ToLower(strings.TrimSpace(word))
	switch word {
	case "plugged", "plugged-in", "power", "charging":
		word = "plugged-in"
	case "on-battery", "unplug":
		word = "unplugged"
	case "app-open", "app-opens", "opened", "open":
		word = "app-opened"
	case "app-quits", "quit":
		word = "app-quit"
	case "wifi", "wifi-join", "joined":
		word = "wifi-joined"
	case "wifi-leave", "left":
		word = "wifi-left"
	case "disk-low", "space":
		word = "disk"
	case "battery-low":
		word = "battery"
	}
	e, ok := macEvents[word]
	if !ok {
		return nil, fmt.Errorf("%q: %s", spec, macUsage)
	}
	m := &pb.MacTrigger{Event: e}
	minutes := func(def int32) error {
		if val == "" {
			m.Minutes = def
			return nil
		}
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(val), "min"), "m"))
		if err != nil || n < 0 || n > 7*24*60 {
			return fmt.Errorf("%q: minutes, 0 to %d", spec, 7*24*60)
		}
		m.Minutes = int32(n)
		return nil
	}
	switch e {
	case pb.MacTrigger_EVENT_DISK_LOW:
		if i := strings.LastIndex(val, "@"); i >= 0 {
			val, m.Volume = strings.TrimSpace(val[:i]), strings.TrimSpace(val[i+1:])
		}
		low := strings.ToLower(strings.ReplaceAll(val, " ", ""))
		switch {
		case low == "":
			m.Percent = MacDiskPercentDefault
		case strings.HasSuffix(low, "gb") || strings.HasSuffix(low, "g"):
			n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSuffix(low, "b"), "g"))
			if err != nil || n < 1 || n > 1<<20 {
				return nil, fmt.Errorf("%q: mac:disk<NGB, at least 1 GB", spec)
			}
			m.Gigabytes = int32(n)
		default:
			n, err := strconv.Atoi(strings.TrimSuffix(low, "%"))
			if err != nil || n < 1 || n > 99 {
				return nil, fmt.Errorf("%q: mac:disk<N%% (1 to 99) or mac:disk<NGB", spec)
			}
			m.Percent = int32(n)
		}
	case pb.MacTrigger_EVENT_BATTERY_LOW:
		if val == "" {
			m.Percent = MacBatteryPercentDefault
			break
		}
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(val), "%"))
		if err != nil || n < 1 || n > 99 {
			return nil, fmt.Errorf("%q: mac:battery<N%% (1 to 99)", spec)
		}
		m.Percent = int32(n)
	case pb.MacTrigger_EVENT_WAKE:
		if err := minutes(0); err != nil {
			return nil, err
		}
	case pb.MacTrigger_EVENT_AWAY:
		if err := minutes(MacAwayDefault); err != nil {
			return nil, err
		}
		if m.Minutes < 1 {
			return nil, fmt.Errorf("%q: mac:away=MINUTES, at least 1", spec)
		}
	case pb.MacTrigger_EVENT_BACK:
		if err := minutes(MacBackDefault); err != nil {
			return nil, err
		}
	case pb.MacTrigger_EVENT_APP_OPENED, pb.MacTrigger_EVENT_APP_QUIT:
		if val == "" {
			return nil, fmt.Errorf("%q: mac:%s=NAME, the app's name or bundle id", spec, word)
		}
		m.Name = val
	case pb.MacTrigger_EVENT_DRIVE, pb.MacTrigger_EVENT_DISPLAY, pb.MacTrigger_EVENT_WIFI_JOINED, pb.MacTrigger_EVENT_WIFI_LEFT:
		m.Name = val
	default:
		if val != "" {
			return nil, fmt.Errorf("%q: mac:%s takes nothing after it", spec, word)
		}
	}
	return m, nil
}

// DiskWords names a disk for words: "The startup disk", "/Volumes/Backup".
func DiskWords(volume string) string {
	if volume == "" || volume == "/" {
		return "The startup disk"
	}
	return tildeHome(volume)
}

func describeMac(m *pb.MacTrigger) string {
	named := func(what, name string) string {
		if name == "" {
			return what
		}
		return what + " “" + name + "”"
	}
	switch m.Event {
	case pb.MacTrigger_EVENT_DISK_LOW:
		if m.Gigabytes > 0 {
			return fmt.Sprintf("%s has less than %d GB free", DiskWords(m.Volume), m.Gigabytes)
		}
		return fmt.Sprintf("%s has less than %d%% free", DiskWords(m.Volume), cmpInt(m.Percent, MacDiskPercentDefault))
	case pb.MacTrigger_EVENT_BATTERY_LOW:
		return fmt.Sprintf("The battery falls below %d%%", cmpInt(m.Percent, MacBatteryPercentDefault))
	case pb.MacTrigger_EVENT_PLUGGED_IN:
		return "The Mac is plugged in"
	case pb.MacTrigger_EVENT_UNPLUGGED:
		return "The Mac is unplugged"
	case pb.MacTrigger_EVENT_WAKE:
		if m.Minutes > 0 {
			return "The Mac wakes after sleeping " + plural(int(m.Minutes), "minute") + " or more"
		}
		return "The Mac wakes"
	case pb.MacTrigger_EVENT_AWAY:
		return "I’ve been away from the Mac for " + plural(int(cmpInt(m.Minutes, MacAwayDefault)), "minute")
	case pb.MacTrigger_EVENT_BACK:
		return "I’m back at the Mac after " + plural(int(BackMinutes(m)), "minute") + " or more away"
	case pb.MacTrigger_EVENT_DRIVE:
		if m.Name != "" {
			return "A drive named “" + m.Name + "” is connected"
		}
		return "A drive is connected"
	case pb.MacTrigger_EVENT_WIFI_JOINED:
		if m.Name == "" {
			return "The Mac joins a Wi-Fi network"
		}
		return named("The Mac joins the Wi-Fi", m.Name)
	case pb.MacTrigger_EVENT_WIFI_LEFT:
		if m.Name == "" {
			return "The Mac leaves a Wi-Fi network"
		}
		return named("The Mac leaves the Wi-Fi", m.Name)
	case pb.MacTrigger_EVENT_APP_OPENED:
		return "“" + m.Name + "” opens"
	case pb.MacTrigger_EVENT_APP_QUIT:
		return "“" + m.Name + "” quits"
	case pb.MacTrigger_EVENT_DISPLAY:
		if m.Name != "" {
			return "A display named “" + m.Name + "” is connected"
		}
		return "A display is connected"
	case pb.MacTrigger_EVENT_PLACE_ARRIVE:
		return "The Mac arrives at “" + m.Name + "”"
	case pb.MacTrigger_EVENT_PLACE_LEAVE:
		return "The Mac leaves “" + m.Name + "”"
	}
	return "Something on this Mac"
}

// BackMinutes is how long you must have been away for a back trigger.
func BackMinutes(m *pb.MacTrigger) int32 {
	if m.GetMinutes() <= 0 {
		return MacBackDefault
	}
	return m.GetMinutes()
}

func specMac(m *pb.MacTrigger) string {
	switch m.Event {
	case pb.MacTrigger_EVENT_PLACE_ARRIVE:
		return "place:arrive=" + m.Name
	case pb.MacTrigger_EVENT_PLACE_LEAVE:
		return "place:leave=" + m.Name
	}
	s := "mac:" + MacEventName(m.Event)
	switch m.Event {
	case pb.MacTrigger_EVENT_DISK_LOW:
		if m.Gigabytes > 0 {
			s += "<" + strconv.Itoa(int(m.Gigabytes)) + "GB"
		} else {
			s += "<" + strconv.Itoa(int(cmpInt(m.Percent, MacDiskPercentDefault))) + "%"
		}
		if m.Volume != "" {
			s += "@" + m.Volume
		}
	case pb.MacTrigger_EVENT_BATTERY_LOW:
		s += "<" + strconv.Itoa(int(cmpInt(m.Percent, MacBatteryPercentDefault))) + "%"
	case pb.MacTrigger_EVENT_WAKE, pb.MacTrigger_EVENT_AWAY, pb.MacTrigger_EVENT_BACK:
		if m.Minutes > 0 {
			s += "=" + strconv.Itoa(int(m.Minutes))
		}
	default:
		if m.Name != "" {
			s += "=" + m.Name
		}
	}
	return s
}

func cmpInt(v, def int32) int32 {
	if v <= 0 {
		return def
	}
	return v
}

// ---------- Only If ----------

// ConditionUsage lists the conditions ParseCondition reads.
const ConditionUsage = "power, away[=MINUTES], wifi=NAME, place=NAME, hours=HH:MM-HH:MM, days=weekdays|weekends|mon,wed,…, free or disk>N%"

var dayNames = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// ParseDays reads days: daily, weekdays, weekends, mon-fri, or a list
// (mon,wed,fri). None (nil): every day.
func ParseDays(v string) ([]int32, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "daily", "every", "everyday", "all":
		return nil, nil
	case "weekdays":
		return []int32{1, 2, 3, 4, 5}, nil
	case "weekends":
		return []int32{0, 6}, nil
	}
	day := func(d string) int {
		d = strings.TrimSpace(d)
		for i, n := range dayNames {
			if len(d) >= 3 && strings.HasPrefix(d, n) {
				return i
			}
		}
		return -1
	}
	bad := fmt.Errorf("%q: daily, weekdays, weekends, mon-fri, or days like mon,wed,fri", v)
	seen := map[int32]bool{}
	for _, part := range strings.Split(v, ",") {
		if a, b, ok := strings.Cut(part, "-"); ok {
			from, to := day(a), day(b)
			if from < 0 || to < 0 {
				return nil, bad
			}
			for i := from; ; i = (i + 1) % 7 {
				seen[int32(i)] = true
				if i == to {
					break
				}
			}
			continue
		}
		i := day(part)
		if i < 0 {
			return nil, bad
		}
		seen[int32(i)] = true
	}
	var out []int32
	for i := int32(0); i < 7; i++ {
		if seen[i] {
			out = append(out, i)
		}
	}
	return out, nil
}

// DaysWords says days: "weekdays", "weekends", "every day", "Mon, Wed".
func DaysWords(days []int32) string {
	set := map[int32]bool{}
	for _, d := range days {
		set[d] = true
	}
	switch {
	case len(set) == 0 || len(set) == 7:
		return "every day"
	case len(set) == 5 && !set[0] && !set[6]:
		return "weekdays"
	case len(set) == 2 && set[0] && set[6]:
		return "weekends"
	}
	var w []string
	for _, d := range []int32{1, 2, 3, 4, 5, 6, 0} { // Monday first
		if set[d] {
			w = append(w, strings.ToUpper(dayNames[d][:1])+dayNames[d][1:])
		}
	}
	return strings.Join(w, ", ")
}

// daysSpec writes days as ParseDays reads them.
func daysSpec(days []int32) string {
	switch DaysWords(days) {
	case "every day":
		return "daily"
	case "weekdays":
		return "weekdays"
	case "weekends":
		return "weekends"
	}
	var w []string
	for _, d := range days {
		w = append(w, dayNames[d])
	}
	return strings.Join(w, ",")
}

// ParseHHMM reads a time of day, "09:30", as minutes past midnight.
func ParseHHMM(v string) (int32, error) {
	h, m, ok := strings.Cut(strings.TrimSpace(v), ":")
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || hh < 0 || hh > 24 || mm < 0 || mm > 59 || hh == 24 && mm != 0 {
		return 0, fmt.Errorf("%q isn't a time like 09:30", v)
	}
	return int32(hh*60 + mm), nil
}

// HHMM writes minutes past midnight as "09:30".
func HHMM(m int32) string { return fmt.Sprintf("%02d:%02d", m/60, m%60) }

// ParseHours reads "HH:MM-HH:MM" (the second before the first: through
// midnight).
func ParseHours(v string) (from, to int32, err error) {
	a, b, ok := strings.Cut(v, "-")
	if !ok {
		return 0, 0, fmt.Errorf("%q: hours like 09:00-17:30", v)
	}
	if from, err = ParseHHMM(a); err != nil {
		return 0, 0, err
	}
	if to, err = ParseHHMM(b); err != nil {
		return 0, 0, err
	}
	if from == to {
		return 0, 0, fmt.Errorf("%q: the hours start and end at the same time", v)
	}
	return from, to, nil
}

// InHours: minute (past midnight) is from from up to to (through
// midnight when to is before from).
func InHours(minute, from, to int32) bool {
	if from <= to {
		return minute >= from && minute < to
	}
	return minute >= from || minute < to
}

// ParseCondition reads one Only If condition (ConditionUsage).
func ParseCondition(spec string) (*pb.Condition, error) {
	spec = strings.TrimSpace(spec)
	word, val := spec, ""
	if i := strings.IndexAny(spec, "=>"); i >= 0 {
		word, val = spec[:i], strings.TrimSpace(spec[i+1:])
	}
	word = strings.ToLower(strings.TrimSpace(word))
	c := &pb.Condition{}
	switch word {
	case "power", "plugged-in", "plugged", "charging":
		c.Kind = pb.Condition_KIND_POWER
	case "away":
		c.Kind = pb.Condition_KIND_AWAY
		if val != "" {
			n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSuffix(val, "min"), "m"))
			if err != nil || n < 0 || n > 7*24*60 {
				return nil, fmt.Errorf("%q: away=MINUTES", spec)
			}
			c.Minutes = int32(n)
		}
	case "wifi", "wi-fi":
		c.Kind = pb.Condition_KIND_WIFI
		if val == "" {
			return nil, fmt.Errorf("%q: wifi=NAME, the network's name", spec)
		}
		c.Wifi = val
	case "place", "at":
		c.Kind = pb.Condition_KIND_PLACE
		if val == "" {
			return nil, fmt.Errorf("%q: place=NAME, one of the Mac app's Places", spec)
		}
		c.Place = val
	case "hours", "between":
		c.Kind = pb.Condition_KIND_HOURS
		var err error
		if c.FromMinute, c.ToMinute, err = ParseHours(val); err != nil {
			return nil, err
		}
	case "days", "on":
		c.Kind = pb.Condition_KIND_DAYS
		days, err := ParseDays(val)
		if err != nil {
			return nil, err
		}
		if len(days) == 0 {
			return nil, errors.New("days=daily always holds: leave it out")
		}
		c.Weekdays = days
	case "free", "not-busy":
		c.Kind = pb.Condition_KIND_FREE
	case "disk":
		c.Kind = pb.Condition_KIND_DISK
		n, err := strconv.Atoi(strings.TrimSuffix(val, "%"))
		if err != nil || n < 1 || n > 99 {
			return nil, fmt.Errorf("%q: disk>N%% (1 to 99)", spec)
		}
		c.Percent = int32(n)
	default:
		return nil, fmt.Errorf("only if %q: one of %s", spec, ConditionUsage)
	}
	return c, nil
}

// SpecCondition writes c as ParseCondition reads it.
func SpecCondition(c *pb.Condition) string {
	switch c.Kind {
	case pb.Condition_KIND_POWER:
		return "power"
	case pb.Condition_KIND_AWAY:
		if c.Minutes > 0 {
			return "away=" + strconv.Itoa(int(c.Minutes))
		}
		return "away"
	case pb.Condition_KIND_WIFI:
		return "wifi=" + c.Wifi
	case pb.Condition_KIND_PLACE:
		return "place=" + c.Place
	case pb.Condition_KIND_HOURS:
		return "hours=" + HHMM(c.FromMinute) + "-" + HHMM(c.ToMinute)
	case pb.Condition_KIND_DAYS:
		return "days=" + daysSpec(c.Weekdays)
	case pb.Condition_KIND_FREE:
		return "free"
	case pb.Condition_KIND_DISK:
		return "disk>" + strconv.Itoa(int(c.Percent)) + "%"
	}
	return ""
}

// DescribeCondition says c in words, to follow "Only if": "plugged in",
// "on the Wi-Fi “Home”".
func DescribeCondition(c *pb.Condition) string {
	switch c.Kind {
	case pb.Condition_KIND_POWER:
		return "the Mac is plugged in"
	case pb.Condition_KIND_AWAY:
		if c.Minutes > 0 {
			return "I’ve been away for " + plural(int(c.Minutes), "minute")
		}
		return "I’m away from the Mac"
	case pb.Condition_KIND_WIFI:
		return "on the Wi-Fi “" + c.Wifi + "”"
	case pb.Condition_KIND_PLACE:
		return "the Mac is at “" + c.Place + "”"
	case pb.Condition_KIND_HOURS:
		return "between " + HHMM(c.FromMinute) + " and " + HHMM(c.ToMinute)
	case pb.Condition_KIND_DAYS:
		return "on " + DaysWords(c.Weekdays)
	case pb.Condition_KIND_FREE:
		return "I’m not in a meeting"
	case pb.Condition_KIND_DISK:
		return "the startup disk has more than " + strconv.Itoa(int(c.Percent)) + "% free"
	}
	return "an unknown condition"
}

// OnlyIfWords is all of s's conditions in a line: "plugged in and on
// weekdays" ("" for none).
func OnlyIfWords(conds []*pb.Condition) string {
	var w []string
	for _, c := range conds {
		w = append(w, DescribeCondition(c))
	}
	return strings.Join(w, " and ")
}

// ConditionMacOnly: c needs a Mac (power, Wi-Fi, a calendar, you away).
func ConditionMacOnly(c *pb.Condition) bool {
	switch c.Kind {
	case pb.Condition_KIND_POWER, pb.Condition_KIND_WIFI, pb.Condition_KIND_FREE, pb.Condition_KIND_AWAY, pb.Condition_KIND_PLACE:
		return true
	}
	return false
}
