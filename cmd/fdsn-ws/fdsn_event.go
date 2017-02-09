package main

import (
	"bytes"
	"database/sql"
	"fmt"
	"github.com/GeoNet/weft"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// supported query parameters for the event service from http://www.fdsn.org/webservices/FDSN-WS-Specifications-1.1.pdf
type fdsnEventV1 struct {
	PublicID     string  `schema:"eventid"`      // select a specific event by ID; event identifiers are data center specific.
	MinLatitude  float64 `schema:"minlatitude"`  // limit to events with a latitude larger than or equal to the specified minimum.
	MaxLatitude  float64 `schema:"maxlatitude"`  // limit to events with a latitude smaller than or equal to the specified maximum.
	MinLongitude float64 `schema:"minlongitude"` // limit to events with a longitude larger than or equal to the specified minimum.
	MaxLongitude float64 `schema:"maxlongitude"` // limit to events with a longitude smaller than or equal to the specified maximum.
	MinDepth     float64 `schema:"mindepth"`     // limit to events with depth more than the specified minimum.
	MaxDepth     float64 `schema:"maxdepth"`     // limit to events with depth less than the specified maximum.
	MinMagnitude float64 `schema:"minmagnitude"` // limit to events with a magnitude larger than the specified minimum.
	MaxMagnitude float64 `schema:"maxmagnitude"` // limit to events with a magnitude smaller than the specified maximum.
	OrderBy      string  `schema:"orderby"`      // order the result by time or magnitude with the following possibilities: time, time-asc, magnitude, magnitude-asc
	StartTime    Time    `schema:"starttime"`    // limit to events on or after the specified start time.
	EndTime      Time    `schema:"endtime"`      // limit to events on or before the specified end time.
}

type Time struct {
	time.Time
}

var fdsnEventWadlFile []byte
var fdsnEventIndex []byte

func init() {
	var err error
	fdsnEventWadlFile, err = ioutil.ReadFile("assets/fdsn-ws-event.wadl")
	if err != nil {
		log.Printf("error reading assets/fdsn-ws-event.wadl: %s", err.Error())
	}

	fdsnEventIndex, err = ioutil.ReadFile("assets/fdsn-ws-event.html")
	if err != nil {
		log.Printf("error reading assets/fdsn-ws-event.html: %s", err.Error())
	}
}

/*
parses the time in text as per the FDSN spec.  Pads text for parsing with
time.RFC3339Nano.  Accepted formats are (UTC):
   YYYY-MM-DDTHH:MM:SS.ssssss
   YYYY-MM-DDTHH:MM:SS
   YYYY-MM-DD

Implements the encoding.TextUnmarshaler interface.
*/
func (t *Time) UnmarshalText(text []byte) (err error) {
	s := string(text)

	switch len(s) {
	case 26:
		s = s + "000Z" // YYYY-MM-DDTHH:MM:SS.ssssss
	case 19:
		s = s + ".000000000Z" // YYYY-MM-DDTHH:MM:SS
	case 10:
		s = s + "T00:00:00.000000000Z" // YYYY-MM-DD
	default:
		return fmt.Errorf("invalid time format: %s", s)
	}

	t.Time, err = time.Parse(time.RFC3339Nano, s)
	return
}

func parseEventV1(v url.Values) (fdsnEventV1, error) {
	// All query parameters are optional and float zero values overlap
	// with possible request ranges so the default is set to the max float val.
	e := fdsnEventV1{
		MinLatitude:  math.MaxFloat64,
		MaxLatitude:  math.MaxFloat64,
		MinLongitude: math.MaxFloat64,
		MaxLongitude: math.MaxFloat64,
		MinDepth:     math.MaxFloat64,
		MaxDepth:     math.MaxFloat64,
		MinMagnitude: math.MaxFloat64,
		MaxMagnitude: math.MaxFloat64,
	}

	err := decoder.Decode(&e, v)
	if err != nil {
		return e, err
	}

	// geometry bounds checking
	if e.MinLatitude != math.MaxFloat64 && e.MinLatitude < -90.0 {
		err = fmt.Errorf("minlatitude < -90.0: %f", e.MinLatitude)
		return e, err
	}

	if e.MaxLatitude != math.MaxFloat64 && e.MaxLatitude > 90.0 {
		err = fmt.Errorf("maxlatitude > 90.0: %f", e.MaxLatitude)
		return e, err
	}

	if e.MinLongitude != math.MaxFloat64 && e.MinLongitude < -180.0 {
		err = fmt.Errorf("minlongitude < -180.0: %f", e.MinLongitude)
		return e, err
	}

	if e.MaxLongitude != math.MaxFloat64 && e.MaxLongitude > 180.0 {
		err = fmt.Errorf("maxlongitude > 180.0: %f", e.MaxLongitude)
		return e, err
	}

	switch e.OrderBy {
	case "", "time", "time-asc", "magnitude", "magnitude-asc":
	default:
		err = fmt.Errorf("invalid option for orderby: %s", e.OrderBy)
	}

	return e, err
}

// query queries the DB for events matching e.
// The caller must close sql.Rows.
func (e *fdsnEventV1) query() (*sql.Rows, error) {
	q := "SELECT Quakeml12Event FROM fdsn.event WHERE deleted != true"

	qq, args := e.filter()

	if qq != "" {
		q = q + " AND " + qq
	}

	switch e.OrderBy {
	case "":
	case "time":
		q += " ORDER BY origintime desc"
	case "time-asc":
		q += " ORDER BY origintime asc"
	case "magnitude":
		q += " ORDER BY magnitude desc"
	case "magnitude-asc":
		q += " ORDER BY magnitude desc"
	}

	return db.Query(q, args...)
}

// query returns a count of events in the DB for e.
func (e *fdsnEventV1) count() (int, error) {
	q := "SELECT count(*) FROM fdsn.event WHERE deleted != true"

	qq, args := e.filter()

	if qq != "" {
		q = q + " AND " + qq
	}

	var c int
	err := db.QueryRow(q, args...).Scan(&c)

	return c, err
}

func (e *fdsnEventV1) filter() (q string, args []interface{}) {
	i := 1

	if e.PublicID != "" {
		q = fmt.Sprintf("%s publicid = $%d AND", q, i)
		args = append(args, e.PublicID)
		i++
	}

	if e.MinLatitude != math.MaxFloat64 {
		q = fmt.Sprintf("%s latitude >= $%d AND", q, i)
		args = append(args, e.MinLatitude)
		i++
	}

	if e.MaxLatitude != math.MaxFloat64 {
		q = fmt.Sprintf("%s latitude <= $%d AND", q, i)
		args = append(args, e.MaxLatitude)
		i++
	}

	if e.MinLongitude != math.MaxFloat64 {
		q = fmt.Sprintf("%s longitude >= $%d AND", q, i)
		args = append(args, e.MinLongitude)
		i++
	}

	if e.MaxLongitude != math.MaxFloat64 {
		q = fmt.Sprintf("%s longitude <= $%d AND", q, i)
		args = append(args, e.MaxLongitude)
		i++
	}

	if e.MinDepth != math.MaxFloat64 {
		q = fmt.Sprintf("%s depth > $%d AND", q, i)
		args = append(args, e.MinDepth)
		i++
	}

	if e.MaxDepth != math.MaxFloat64 {
		q = fmt.Sprintf("%s depth < $%d AND", q, i)
		args = append(args, e.MaxDepth)
		i++
	}

	if e.MinMagnitude != math.MaxFloat64 {
		q = fmt.Sprintf("%s magnitude > $%d AND", q, i)
		args = append(args, e.MinMagnitude)
		i++
	}

	if e.MaxMagnitude != math.MaxFloat64 {
		q = fmt.Sprintf("%s magnitude < $%d AND", q, i)
		args = append(args, e.MaxMagnitude)
		i++
	}

	if !e.StartTime.Time.IsZero() {
		q = fmt.Sprintf("%s origintime >= $%d AND", q, i)
		args = append(args, e.StartTime.Time)
		i++
	}

	if !e.EndTime.Time.IsZero() {
		q = fmt.Sprintf("%s origintime <= $%d AND", q, i)
		args = append(args, e.EndTime.Time)
		i++
	}

	q = strings.TrimSuffix(q, " AND")

	return
}

/*
eventV1Handler assembles QuakeML event fragments from the DB into a complete
QuakeML event.  The result set is limited to 10,000 events which will be ~1.2GB.
*/
func fdsnEventV1Handler(r *http.Request, h http.Header, b *bytes.Buffer) *weft.Result {
	if r.Method != "GET" {
		return &weft.MethodNotAllowed
	}

	e, err := parseEventV1(r.URL.Query())
	if err != nil {
		return weft.BadRequest(err.Error())
	}

	c, err := e.count()
	if err != nil {
		return weft.ServiceUnavailableError(err)
	}

	if c > 10000 {
		return &weft.Result{
			Code: http.StatusRequestEntityTooLarge,
			Msg:  fmt.Sprintf("result to large found %d events, limit is 10,000", c),
		}
	}

	rows, err := e.query()
	if err != nil {
		return weft.ServiceUnavailableError(err)
	}
	defer rows.Close()

	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
	<q:quakeml xmlns:q="http://quakeml.org/xmlns/quakeml/1.2" xmlns="http://quakeml.org/xmlns/bed/1.2">
	  <eventParameters publicID="smi:nz.org.geonet/NA">`)

	var xml string

	for rows.Next() {
		err = rows.Scan(&xml)
		if err != nil {
			return weft.ServiceUnavailableError(err)
		}

		b.WriteString(xml)
	}

	b.WriteString(`</eventParameters></q:quakeml>`)

	log.Printf("%s found %d events, result size %.1f (MB)", r.RequestURI, c, float64(b.Len())/1000000.0)

	h.Set("Content-Type", "application/xml")

	return &weft.StatusOK
}

func fdsnEventVersion(r *http.Request, h http.Header, b *bytes.Buffer) *weft.Result {
	switch r.Method {
	case "GET":
		if res := weft.CheckQuery(r, []string{}, []string{}); !res.Ok {
			return res
		}

		h.Set("Content-Type", "text/plain")
		b.WriteString("1.1")
		return &weft.StatusOK
	default:
		return &weft.MethodNotAllowed
	}
}

func fdsnEventContributors(r *http.Request, h http.Header, b *bytes.Buffer) *weft.Result {
	switch r.Method {
	case "GET":
		if res := weft.CheckQuery(r, []string{}, []string{}); !res.Ok {
			return res
		}

		h.Set("Content-Type", "application/xml")
		b.WriteString(`<Contributors><Contributor>WEL</Contributor></Contributors>`)
		return &weft.StatusOK
	default:
		return &weft.MethodNotAllowed
	}
}

func fdsnEventCatalogs(r *http.Request, h http.Header, b *bytes.Buffer) *weft.Result {
	switch r.Method {
	case "GET":
		if res := weft.CheckQuery(r, []string{}, []string{}); !res.Ok {
			return res
		}

		h.Set("Content-Type", "application/xml")
		b.WriteString(`<Catalogs><Catalog>GeoNet</Catalog></Catalogs>`)
		return &weft.StatusOK
	default:
		return &weft.MethodNotAllowed
	}
}

func fdsnEventWadl(r *http.Request, h http.Header, b *bytes.Buffer) *weft.Result {
	switch r.Method {
	case "GET":
		if res := weft.CheckQuery(r, []string{}, []string{}); !res.Ok {
			return res
		}

		h.Set("Content-Type", "application/xml")
		b.Write(fdsnEventWadlFile)
		return &weft.StatusOK
	default:
		return &weft.MethodNotAllowed
	}
}

func fdsnEventV1Index(r *http.Request, h http.Header, b *bytes.Buffer) *weft.Result {
	switch r.Method {
	case "GET":
		if res := weft.CheckQuery(r, []string{}, []string{}); !res.Ok {
			return res
		}

		h.Set("Content-Type", "text/html")
		b.Write(fdsnEventIndex)
		return &weft.StatusOK
	default:
		return &weft.MethodNotAllowed
	}
}