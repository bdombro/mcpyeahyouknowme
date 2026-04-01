package google_places

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	defaultPlacesBaseURL = "https://places.googleapis.com/v1"
	searchFieldMask      = "places.id,places.displayName,places.formattedAddress,places.types,places.location,places.businessStatus"
	detailsFieldMask     = "id,displayName,formattedAddress,nationalPhoneNumber,internationalPhoneNumber,websiteUri,googleMapsUri,location,regularOpeningHours,rating,userRatingCount,addressComponents,businessStatus,types,priceLevel"
)

var errPlacesAPIKeyMissing = errors.New("GOOGLE_PLACE_API_KEY is not configured in this build")

// GooglePlaceAPIKey is injected at build time via ldflags.
var GooglePlaceAPIKey string

type PlacesClient struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
}

type Coordinates struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type AddressComponent struct {
	LongText  string   `json:"long_text,omitempty"`
	ShortText string   `json:"short_text,omitempty"`
	Types     []string `json:"types,omitempty"`
}

type OpeningHours struct {
	OpenNow             *bool    `json:"open_now,omitempty"`
	WeekdayDescriptions []string `json:"weekday_descriptions,omitempty"`
}

type PlaceSummary struct {
	PlaceID          string       `json:"place_id"`
	DisplayName      string       `json:"display_name,omitempty"`
	FormattedAddress string       `json:"formatted_address,omitempty"`
	Types            []string     `json:"types,omitempty"`
	Location         *Coordinates `json:"location,omitempty"`
	BusinessStatus   string       `json:"business_status,omitempty"`
}

type PlaceDetails struct {
	PlaceID                  string             `json:"place_id"`
	DisplayName              string             `json:"display_name,omitempty"`
	FormattedAddress         string             `json:"formatted_address,omitempty"`
	NationalPhoneNumber      string             `json:"national_phone_number,omitempty"`
	InternationalPhoneNumber string             `json:"international_phone_number,omitempty"`
	WebsiteURI               string             `json:"website_uri,omitempty"`
	GoogleMapsURI            string             `json:"google_maps_uri,omitempty"`
	Location                 *Coordinates       `json:"location,omitempty"`
	OpeningHours             *OpeningHours      `json:"opening_hours,omitempty"`
	Rating                   *float64           `json:"rating,omitempty"`
	UserRatingCount          *int               `json:"user_rating_count,omitempty"`
	AddressComponents        []AddressComponent `json:"address_components,omitempty"`
	BusinessStatus           string             `json:"business_status,omitempty"`
	Types                    []string           `json:"types,omitempty"`
	PriceLevel               string             `json:"price_level,omitempty"`
}

type searchTextRequest struct {
	TextQuery string `json:"textQuery"`
	PageSize  int    `json:"pageSize,omitempty"`
}

type searchTextResponse struct {
	Places []placeRecord `json:"places"`
}

type placeRecord struct {
	ID                       string                `json:"id"`
	DisplayName              localizedText         `json:"displayName"`
	FormattedAddress         string                `json:"formattedAddress"`
	Types                    []string              `json:"types"`
	Location                 latLng                `json:"location"`
	BusinessStatus           string                `json:"businessStatus"`
	NationalPhoneNumber      string                `json:"nationalPhoneNumber"`
	InternationalPhoneNumber string                `json:"internationalPhoneNumber"`
	WebsiteURI               string                `json:"websiteUri"`
	GoogleMapsURI            string                `json:"googleMapsUri"`
	RegularOpeningHours      *regularOpeningHours  `json:"regularOpeningHours"`
	Rating                   *float64              `json:"rating"`
	UserRatingCount          *int                  `json:"userRatingCount"`
	AddressComponents        []apiAddressComponent `json:"addressComponents"`
	PriceLevel               string                `json:"priceLevel"`
}

type localizedText struct {
	Text string `json:"text"`
}

type latLng struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type regularOpeningHours struct {
	OpenNow             *bool    `json:"openNow"`
	WeekdayDescriptions []string `json:"weekdayDescriptions"`
}

type apiAddressComponent struct {
	LongText  string   `json:"longText"`
	ShortText string   `json:"shortText"`
	Types     []string `json:"types"`
}

type apiErrorEnvelope struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *PlacesClient) SearchPlaces(ctx context.Context, query string, maxResults int) ([]PlaceSummary, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("query is required")
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	if maxResults <= 0 {
		maxResults = 5
	}
	if maxResults > 10 {
		maxResults = 10
	}

	reqBody, err := json.Marshal(searchTextRequest{
		TextQuery: query,
		PageSize:  maxResults,
	})
	if err != nil { // nocov
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/places:searchText", bytes.NewReader(reqBody))
	if err != nil { // nocov
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Goog-Api-Key", c.apiKey)
	req.Header.Set("X-Goog-FieldMask", searchFieldMask)

	var resp searchTextResponse
	if err := c.do(req, &resp); err != nil {
		return nil, err
	}

	places := make([]PlaceSummary, 0, len(resp.Places))
	for _, place := range resp.Places {
		places = append(places, place.toSummary())
	}
	return places, nil
}

func (c *PlacesClient) GetPlace(ctx context.Context, placeID string) (*PlaceDetails, error) {
	if strings.TrimSpace(placeID) == "" {
		return nil, errors.New("place_id is required")
	}
	if err := c.validate(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		c.baseURL+"/places/"+url.PathEscape(placeID),
		nil,
	)
	if err != nil { // nocov
		return nil, err
	}
	req.Header.Set("X-Goog-Api-Key", c.apiKey)
	req.Header.Set("X-Goog-FieldMask", detailsFieldMask)

	var place placeRecord
	if err := c.do(req, &place); err != nil {
		return nil, err
	}

	details := place.toDetails()
	return &details, nil
}

func (c *PlacesClient) validate() error {
	if c == nil || c.apiKey == "" {
		return errPlacesAPIKeyMissing
	}
	return nil
}

func (c *PlacesClient) do(req *http.Request, dest interface{}) error {
	resp, err := c.httpClient.Do(req)
	if err != nil { // nocov
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil { // nocov
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return apiError(resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, dest); err != nil {
		return fmt.Errorf("decode Google Places response: %w", err)
	}
	return nil
}

func apiError(statusCode int, body []byte) error {
	var envelope apiErrorEnvelope
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Error.Message != "" {
		return fmt.Errorf("Google Places API error (%d): %s", statusCode, envelope.Error.Message)
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		text = http.StatusText(statusCode)
	}
	return fmt.Errorf("Google Places API error (%d): %s", statusCode, text)
}

func (p placeRecord) toSummary() PlaceSummary {
	return PlaceSummary{
		PlaceID:          p.ID,
		DisplayName:      p.DisplayName.Text,
		FormattedAddress: p.FormattedAddress,
		Types:            p.Types,
		Location:         locationPtr(p.Location),
		BusinessStatus:   p.BusinessStatus,
	}
}

func (p placeRecord) toDetails() PlaceDetails {
	details := PlaceDetails{
		PlaceID:                  p.ID,
		DisplayName:              p.DisplayName.Text,
		FormattedAddress:         p.FormattedAddress,
		NationalPhoneNumber:      p.NationalPhoneNumber,
		InternationalPhoneNumber: p.InternationalPhoneNumber,
		WebsiteURI:               p.WebsiteURI,
		GoogleMapsURI:            p.GoogleMapsURI,
		Location:                 locationPtr(p.Location),
		BusinessStatus:           p.BusinessStatus,
		Types:                    p.Types,
		PriceLevel:               p.PriceLevel,
	}
	if p.RegularOpeningHours != nil {
		details.OpeningHours = &OpeningHours{
			OpenNow:             p.RegularOpeningHours.OpenNow,
			WeekdayDescriptions: p.RegularOpeningHours.WeekdayDescriptions,
		}
	}
	if p.Rating != nil {
		details.Rating = p.Rating
	}
	if p.UserRatingCount != nil {
		details.UserRatingCount = p.UserRatingCount
	}
	if len(p.AddressComponents) > 0 {
		details.AddressComponents = make([]AddressComponent, 0, len(p.AddressComponents))
		for _, comp := range p.AddressComponents {
			details.AddressComponents = append(details.AddressComponents, AddressComponent{
				LongText:  comp.LongText,
				ShortText: comp.ShortText,
				Types:     comp.Types,
			})
		}
	}
	return details
}

func locationPtr(loc latLng) *Coordinates {
	if loc == (latLng{}) {
		return nil
	}
	return &Coordinates{
		Latitude:  loc.Latitude,
		Longitude: loc.Longitude,
	}
}
