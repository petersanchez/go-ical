// Package ical implements the iCalendar file format.
//
// iCalendar is defined in RFC 5545.
package ical

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Params map[string][]string

func (params Params) Get(name string) string {
	if values := params[strings.ToUpper(name)]; len(values) > 0 {
		return values[0]
	}
	return ""
}

func (params Params) Set(name, value string) {
	params[strings.ToUpper(name)] = []string{value}
}

func (params Params) Add(name, value string) {
	name = strings.ToUpper(name)
	params[name] = append(params[name], value)
}

func (params Params) Del(name string) {
	delete(params, strings.ToUpper(name))
}

func (params Params) ValueType() ValueType {
	return ValueType(params.Get(ParamValue))
}

func (params Params) SetValueType(t ValueType) {
	if t == ValueDefault {
		params.Del(ParamValue)
	} else {
		params.Set(ParamValue, string(t))
	}
}

type Property struct {
	Name   string
	Params Params
	Value  string
}

func NewProperty(name string) *Property {
	return &Property{
		Name:   strings.ToUpper(name),
		Params: make(Params),
	}
}

func (prop *Property) expectValueType(want ValueType) error {
	t := prop.Params.ValueType()
	if t != ValueDefault && t != want {
		return fmt.Errorf("ical: expected type %q, got %q", want, t)
	}
	return nil
}

func (prop *Property) Binary() ([]byte, error) {
	if err := prop.expectValueType(ValueBinary); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(prop.Value)
}

func (prop *Property) Bool() (bool, error) {
	if err := prop.expectValueType(ValueBool); err != nil {
		return false, err
	}
	switch strings.ToUpper(prop.Value) {
	case "TRUE":
		return true, nil
	case "FALSE":
		return false, nil
	default:
		return false, fmt.Errorf("ical: invalid boolean: %q", prop.Value)
	}
}

func (prop *Property) DateTime(loc *time.Location) (time.Time, error) {
	if loc == nil {
		loc = time.UTC
	}
	switch t := prop.Params.ValueType(); t {
	case ValueDefault, ValueDateTime:
		// TODO: use the TZID parameter, if any
		if t, err := time.ParseInLocation("20060102T150405", prop.Value, loc); err == nil {
			return t, nil
		}
		return time.ParseInLocation("20060102T150405Z", prop.Value, time.UTC)
	case ValueDate:
		return time.ParseInLocation("20060102", prop.Value, loc)
	default:
		return time.Time{}, fmt.Errorf("ical: expected DATE or DATE-TIME, got %q", t)
	}
}

func (prop *Property) SetDateTime(t time.Time) {
	prop.Params.SetValueType(ValueDateTime)
	prop.Value = t.Format("20060102T150405Z")
}

type durationParser struct {
	s string
}

func (p *durationParser) consume(c byte) bool {
	if len(p.s) == 0 || p.s[0] != c {
		return false
	}
	p.s = p.s[1:]
	return true
}

func (p *durationParser) parseCount() (time.Duration, error) {
	// Find the first non-digit
	i := strings.IndexFunc(p.s, func(r rune) bool {
		return r < '0' || r > '9'
	})
	if i == 0 {
		return 0, fmt.Errorf("ical: invalid duration: expected a digit")
	}
	if i < 0 {
		i = len(p.s)
	}

	n, err := strconv.ParseUint(p.s[:i], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("ical: invalid duration: %v", err)
	}
	p.s = p.s[i:]
	return time.Duration(n), nil
}

func (p *durationParser) parseDuration() (time.Duration, error) {
	neg := p.consume('-')
	if !neg {
		_ = p.consume('+')
	}

	if !p.consume('P') {
		return 0, fmt.Errorf("ical: invalid duration: expected 'P'")
	}

	var dur time.Duration
	isTime := false
	for len(p.s) > 0 {
		if p.consume('T') {
			isTime = true
		}

		n, err := p.parseCount()
		if err != nil {
			return 0, err
		}

		if !isTime {
			if p.consume('D') {
				dur += n * 24 * time.Hour
			} else if p.consume('W') {
				dur += n * 7 * 24 * time.Hour
			} else {
				return 0, fmt.Errorf("ical: invalid duration: expected 'D' or 'W'")
			}
		} else {
			if p.consume('H') {
				dur += n * time.Hour
			} else if p.consume('M') {
				dur += n * time.Minute
			} else if p.consume('S') {
				dur += n * time.Second
			} else {
				return 0, fmt.Errorf("ical: invalid duration: expected 'H', 'M' or 'S'")
			}
		}
	}

	if neg {
		dur = -dur
	}
	return dur, nil
}

func (prop *Property) Duration() (time.Duration, error) {
	if err := prop.expectValueType(ValueDuration); err != nil {
		return 0, err
	}
	p := durationParser{strings.ToUpper(prop.Value)}
	return p.parseDuration()
}

func (prop *Property) SetDuration(dur time.Duration) {
	prop.Params.SetValueType(ValueDuration)

	sec := dur.Milliseconds() / 1000
	neg := sec < 0
	if sec < 0 {
		sec = -sec
	}

	var s string
	if neg {
		s += "-"
	}
	s += "PT"
	s += strconv.FormatInt(sec, 10)
	s += "S"

	prop.Value = s
}

func (prop *Property) Float() (float64, error) {
	if err := prop.expectValueType(ValueFloat); err != nil {
		return 0, err
	}
	return strconv.ParseFloat(prop.Value, 64)
}

func (prop *Property) Int() (int, error) {
	if err := prop.expectValueType(ValueInt); err != nil {
		return 0, err
	}
	return strconv.Atoi(prop.Value)
}

func (prop *Property) Text() (string, error) {
	if err := prop.expectValueType(ValueText); err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.Grow(len(prop.Value))
	for i := 0; i < len(prop.Value); i++ {
		if c := prop.Value[i]; c == '\\' {
			i++
			if i >= len(prop.Value) {
				return "", fmt.Errorf("ical: malformed text: antislash at end of text")
			}
			switch c := prop.Value[i]; c {
			case '\\', ';', ',':
				sb.WriteByte(c)
			case 'N', 'n':
				sb.WriteByte('\n')
			default:
				return "", fmt.Errorf("ical: malformed text: invalid escape sequence '\\%v'", c)
			}
		} else {
			sb.WriteByte(c)
		}
	}

	return sb.String(), nil
}

func (prop *Property) SetText(text string) {
	prop.Params.SetValueType(ValueText)

	var sb strings.Builder
	sb.Grow(len(text))
	for _, r := range text {
		switch r {
		case '\\', ';', ',':
			sb.WriteByte('\\')
			sb.WriteRune(r)
		case '\n':
			sb.WriteString("\\n")
		default:
			sb.WriteRune(r)
		}
	}
	prop.Value = sb.String()
}

// TODO: Period, RecurrenceRule, Time, URI, UTCOffset

type Properties map[string][]Property

func (props Properties) Get(name string) *Property {
	if l := props[strings.ToUpper(name)]; len(l) > 0 {
		return &l[0]
	}
	return nil
}

func (props Properties) Set(prop *Property) {
	props[prop.Name] = []Property{*prop}
}

func (props Properties) Add(prop *Property) {
	props[prop.Name] = append(props[prop.Name], *prop)
}

func (props Properties) Del(name string) {
	delete(props, name)
}

func (props Properties) Text(name string) (string, error) {
	if prop := props.Get(name); prop != nil {
		return prop.Text()
	}
	return "", nil
}

func (props Properties) SetText(name, text string) {
	prop := NewProperty(name)
	prop.SetText(text)
	props.Set(prop)
}

func (props Properties) DateTime(name string, loc *time.Location) (time.Time, error) {
	if prop := props.Get(name); prop != nil {
		return prop.DateTime(loc)
	}
	return time.Time{}, nil
}

func (props Properties) SetDateTime(name string, t time.Time) {
	prop := NewProperty(name)
	prop.SetDateTime(t)
	props.Set(prop)
}

type Component struct {
	Name       string
	Properties Properties
	Children   []*Component
}

func NewComponent(name string) *Component {
	return &Component{
		Name:       strings.ToUpper(name),
		Properties: make(Properties),
	}
}

const (
	CompCalendar = "VCALENDAR"
	CompEvent    = "VEVENT"
	CompToDo     = "VTODO"
	CompJournal  = "VJOURNAL"
	CompFreeBusy = "VFREEBUSY"
	CompTimezone = "VTIMEZONE"
	CompAlarm    = "VALARM"
)

const (
	CompTimezoneStandard = "STANDARD"
	CompTimezoneDaylight = "DAYLIGHT"
)

const (
	// Calendar properties
	PropCalendarScale = "CALSCALE"
	PropMethod        = "METHOD"
	PropProductID     = "PRODID"
	PropVersion       = "VERSION"

	// Component properties
	PropAttach          = "ATTACH"
	PropCategories      = "CATEGORIES"
	PropClass           = "CLASS"
	PropComment         = "COMMENT"
	PropDescription     = "DESCRIPTION"
	PropGeo             = "GEO"
	PropLocation        = "LOCATION"
	PropPercentComplete = "PERCENT-COMPLETE"
	PropPriority        = "PRIORITY"
	PropResources       = "RESOURCES"
	PropStatus          = "STATUS"
	PropSummary         = "SUMMARY"

	// Date and time component properties
	PropCompleted     = "COMPLETED"
	PropDateTimeEnd   = "DTEND"
	PropDue           = "DUE"
	PropDateTimeStart = "DTSTART"
	PropDuration      = "DURATION"
	PropFreeBusy      = "FREEBUSY"
	PropTransparency  = "TRANSP"

	// Timezone component properties
	PropTimezoneID         = "TZID"
	PropTimezoneName       = "TZNAME"
	PropTimezoneOffsetFrom = "TZOFFSETFROM"
	PropTimezoneOffsetTo   = "TZOFFSETTO"
	PropTimezoneURL        = "TZURL"

	// Relationship component properties
	PropAttendee     = "ATTENDEE"
	PropContact      = "CONTACT"
	PropOrganizer    = "ORGANIZER"
	PropRecurrenceID = "RECURRENCE-ID"
	PropRelatedTo    = "RELATED-TO"
	PropURL          = "URL"
	PropUID          = "UID"

	// Recurrence component properties
	PropExceptionDates  = "EXDATE"
	PropRecurrenceDates = "RDATE"
	PropRecurrenceRule  = "RRULE"

	// Alarm component properties
	PropAction  = "ACTION"
	PropRepeat  = "REPEAT"
	PropTrigger = "TRIGGER"

	// Change management component properties
	PropCreated       = "CREATED"
	PropDateTimeStamp = "DTSTAMP"
	PropLastModified  = "LAST-MODIFIED"
	PropSequence      = "SEQUENCE"

	// Miscellaneous component properties
	PropRequestStatus = "REQUEST-STATUS"
)

const (
	ParamAltRep              = "ALTREP"
	ParamCommonName          = "CN"
	ParamCalendarUserType    = "CUTYPE"
	ParamDelegatedFrom       = "DELEGATED-FROM"
	ParamDelegatedTo         = "DELEGATED-TO"
	ParamDir                 = "DIR"
	ParamEncoding            = "ENCODING"
	ParamFormatType          = "FMTTYPE"
	ParamFreeBusyType        = "FBTYPE"
	ParamLanguage            = "LANGUAGE"
	ParamMember              = "MEMBER"
	ParamParticipationStatus = "PARTSTAT"
	ParamRange               = "RANGE"
	ParamRelated             = "RELATED"
	ParamRelationshipType    = "RELTYPE"
	ParamRole                = "ROLE"
	ParamRSVP                = "RSVP"
	ParamSentBy              = "SENT-BY"
	ParamTimezoneID          = "TZID"
	ParamValue               = "VALUE"
)

type ValueType string

const (
	ValueDefault         ValueType = ""
	ValueBinary          ValueType = "BINARY"
	ValueBool            ValueType = "BOOLEAN"
	ValueCalendarAddress ValueType = "CAL-ADDRESS"
	ValueDate            ValueType = "DATE"
	ValueDateTime        ValueType = "DATE-TIME"
	ValueDuration        ValueType = "DURATION"
	ValueFloat           ValueType = "FLOAT"
	ValueInt             ValueType = "INTEGER"
	ValuePeriod          ValueType = "PERIOD"
	ValueRecurrence      ValueType = "RECUR"
	ValueText            ValueType = "TEXT"
	ValueTime            ValueType = "TIME"
	ValueURI             ValueType = "URI"
	ValueUTCOffset       ValueType = "UTC-OFFSET"
)

type Calendar struct {
	*Component
}

func NewCalendar() *Calendar {
	return &Calendar{NewComponent(CompCalendar)}
}

func (cal *Calendar) Events() []Event {
	l := make([]Event, 0, len(cal.Children))
	for _, child := range cal.Children {
		if child.Name == CompEvent {
			l = append(l, Event{child})
		}
	}
	return l
}

type Event struct {
	*Component
}

func NewEvent() *Event {
	return &Event{NewComponent(CompEvent)}
}