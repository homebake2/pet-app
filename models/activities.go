package models

import "encoding/json"

// ActivityEvent represents a single event in the activities response
type ActivityEvent struct {
	ID    string          `json:"id"`
	Date  string          `json:"date"`
	Type  string          `json:"type"`
	Notes *string         `json:"notes,omitempty"`
	Value json.RawMessage `json:"value"`
}

// ActivityDay represents events for a single day
type ActivityDay struct {
	Date   string          `json:"date"`
	Events []ActivityEvent `json:"events"`
}

// ActivitiesResponse is the main response structure for the /activities endpoint
type ActivitiesResponse struct {
	PetName string        `json:"pet_name"`
	Items   []ActivityDay `json:"items"`
}
