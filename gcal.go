// Package gcal implements the github.com/itsabotabot/shared/cal interface for
// Google Calendar.
package gcal

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/net/context"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/calendar/v3"

	"github.com/itsabot/abot/shared/cal"
	"github.com/itsabot/abot/shared/cal/driver"
	"github.com/jmoiron/sqlx"
)

type drv struct{}

func (d *drv) Open(db *sqlx.DB, name string) (driver.Conn, error) {
	// authenticate
}

func init() {
	cal.Register("google", &drv{})
}

type conn struct{}

func (c *conn) GetEvents(driver.TimeRange) {
}

func (c *conn) SaveEvent() error {
}

// event is a Google Calendar event that implements the driver.Event interface.
type event struct {
	Title          string
	Location       string
	StartTime      *time.Time
	DurationInMins int
	Recurring      bool
	RecurringFreq  RecurringFreq
	AllDay         bool
	Attendees      []*Attendee
	UserID         uint64
}

// attendees is a Google Calendar attendee that implements the driver.Attendee
// interface.
type Attendee struct {
	Name  string
	Email string
	Phone string
}

// config is the configuration specification supplied to the OAuth package.
var config = &oauth2.Config{
	ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
	ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
	// Scope determines which API calls you are authorized to make
	Scopes:   []string{"https://www.googleapis.com/auth/calendar"},
	Endpoint: google.Endpoint,
	// Use "postmessage" for the code-flow for server side apps
	RedirectURL: "postmessage",
}

// token represents an OAuth token response.
type token struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	IdToken     string `json:"id_token"`
}

// claimSet represents an IdToken response.
type claimSet struct {
	Sub string
}

// exchange takes an authentication code and exchanges it with the OAuth
// endpoint for a Google API bearer token and a Google+ ID
func exchange(code string) (accessToken string, idToken string, err error) {
	tok, err := config.Exchange(oauth2.NoContext, code)
	if err != nil {
		return "", "", fmt.Errorf("Error while exchanging code: %v", err)
	}
	// TODO: return ID token in second parameter from updated oauth2 interface
	return tok.AccessToken, tok.Extra("id_token").(string), nil
}

// decodeIdToken takes an ID Token and decodes it to fetch the Google+ ID within
func decodeIdToken(idToken string) (gID string, err error) {
	// An ID token is a cryptographically-signed JSON object encoded in base 64.
	// Normally, it is critical that you validate an ID token before you use it,
	// but since you are communicating directly with Google over an
	// intermediary-free HTTPS channel and using your Client Secret to
	// authenticate yourself to Google, you can be confident that the token you
	// receive really comes from Google and is valid. If your server passes the ID
	// token to other components of your app, it is extremely important that the
	// other components validate the token before using it.
	var set ClaimSet
	if idToken != "" {
		// Check that the padding is correct for a base64decode
		parts := strings.Split(idToken, ".")
		if len(parts) < 2 {
			return "", fmt.Errorf("Malformed ID token")
		}
		// Decode the ID token
		b, err := base64Decode(parts[1])
		if err != nil {
			return "", fmt.Errorf("Malformed ID token: %v", err)
		}
		err = json.Unmarshal(b, &set)
		if err != nil {
			return "", fmt.Errorf("Malformed ID token: %v", err)
		}
	}
	return set.Sub, nil
}

func base64Decode(s string) ([]byte, error) {
	// add back missing padding
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	return base64.URLEncoding.DecodeString(s)
}

// Client returns a Google client for communicating with GCal.
func Client(db *sqlx.DB, uid uint64) (*http.Client, error) {
	context := context.Background()
	var token string
	q := `SELECT token FROM sessions WHERE userid=$1 AND label='gcal_token'`
	if err := db.Get(&token, q, uid); err != nil {
		return nil, err
	}
	t := oauth2.Token{}
	t.AccessToken = token
	return config.Client(context, &t), nil
}

// Save a calendar event to GCal.
func (e *Event) Save(client *http.Client) error {
	srv, err := calendar.New(client)
	if err != nil {
		return err
	}
	event := &calendar.Event{
		Summary:  e.Title,
		Location: e.Location,
	}
	if e.AllDay {
		dt := e.StartTime.Format("2006-01-02")
		tz := e.StartTime.Format("-0700")
		event.Start = &calendar.EventDateTime{
			Date:     dt,
			TimeZone: tz,
		}
		event.End = event.Start
	} else {
		dt := e.StartTime.Format(time.RFC3339)
		endTime := e.StartTime.Add(time.Duration(e.DurationInMins) *
			time.Minute)
		dt2 := endTime.Format(time.RFC3339)
		tz := endTime.Format("-0700")
		event.Start = &calendar.EventDateTime{
			DateTime: dt,
			TimeZone: tz,
		}
		event.End = &calendar.EventDateTime{
			DateTime: dt2,
			TimeZone: tz,
		}
	}
	if e.Recurring {
		var freq string
		switch e.RecurringFreq {
		case RecurringFreqDaily:
			freq = "DAILY"
		case RecurringFreqWeekly:
			freq = "WEEKLY"
		case RecurringFreqMonthly:
			freq = "MONTHLY"
		case RecurringFreqYearly:
			freq = "YEARLY"
		}
		event.Recurrence = []string{"RRULE:FREQ=" + freq}
	}
	call := srv.Events.Insert("primary", event)
	_, err = call.Do()
	return err
}
