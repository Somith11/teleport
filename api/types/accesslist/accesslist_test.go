/*
Copyright 2023 Gravitational, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package accesslist

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/gravitational/teleport/api/types/header"
)

func TestParseReviewFrequency(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected ReviewFrequency
	}{
		{input: "1 month", expected: OneMonth},
		{input: "1month", expected: OneMonth},
		{input: "1months", expected: OneMonth},
		{input: "1 m", expected: OneMonth},
		{input: "1m", expected: OneMonth},
		{input: "1", expected: OneMonth},

		{input: "3 month", expected: ThreeMonths},
		{input: "3month", expected: ThreeMonths},
		{input: "3months", expected: ThreeMonths},
		{input: "3 m", expected: ThreeMonths},
		{input: "3m", expected: ThreeMonths},
		{input: "3", expected: ThreeMonths},

		{input: "6 month", expected: SixMonths},
		{input: "6month", expected: SixMonths},
		{input: "6months", expected: SixMonths},
		{input: "6 m", expected: SixMonths},
		{input: "6m", expected: SixMonths},
		{input: "6", expected: SixMonths},

		{input: "12 month", expected: OneYear},
		{input: "12month", expected: OneYear},
		{input: "12months", expected: OneYear},
		{input: "12 m", expected: OneYear},
		{input: "12m", expected: OneYear},
		{input: "12", expected: OneYear},
		{input: "1 year", expected: OneYear},
		{input: "1year", expected: OneYear},
		{input: "1 y", expected: OneYear},
		{input: "1y", expected: OneYear},

		{input: "1 MoNtH", expected: OneMonth},
		{input: "unknown"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.input, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, test.expected, parseReviewFrequency(test.input))
		})
	}
}

func TestParseReviewDayOfMonth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected ReviewDayOfMonth
	}{
		{input: "1", expected: FirstDayOfMonth},
		{input: "first", expected: FirstDayOfMonth},

		{input: "15", expected: FifteenthDayOfMonth},

		{input: "last", expected: LastDayOfMonth},

		{input: "FiRSt", expected: FirstDayOfMonth},
		{input: "unknown"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.input, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, test.expected, parseReviewDayOfMonth(test.input))
		})
	}
}

func TestDeduplicateOwners(t *testing.T) {
	accessList, err := NewAccessList(
		header.Metadata{
			Name: "duplicate test",
		},
		Spec{
			Title:       "title",
			Description: "test access list",
			Owners: []Owner{
				{
					Name:        "test-user1",
					Description: "test user 1",
				},
				{
					Name:        "test-user2",
					Description: "test user 2",
				},
				{
					Name:        "test-user2",
					Description: "duplicate",
				},
			},
			Audit: Audit{
				NextAuditDate: time.Now(),
			},
			MembershipRequires: Requires{
				Roles: []string{"mrole1", "mrole2"},
				Traits: map[string][]string{
					"mtrait1": {"mvalue1", "mvalue2"},
					"mtrait2": {"mvalue3", "mvalue4"},
				},
			},
			OwnershipRequires: Requires{
				Roles: []string{"orole1", "orole2"},
				Traits: map[string][]string{
					"otrait1": {"ovalue1", "ovalue2"},
					"otrait2": {"ovalue3", "ovalue4"},
				},
			},
			Grants: Grants{
				Roles: []string{"grole1", "grole2"},
				Traits: map[string][]string{
					"gtrait1": {"gvalue1", "gvalue2"},
					"gtrait2": {"gvalue3", "gvalue4"},
				},
			},
		},
	)
	require.NoError(t, err)

	require.Len(t, accessList.Spec.Owners, 2)
	require.Equal(t, "test-user1", accessList.Spec.Owners[0].Name)
	require.Equal(t, "test user 1", accessList.Spec.Owners[0].Description)
	require.Equal(t, "test-user2", accessList.Spec.Owners[1].Name)
	require.Equal(t, "test user 2", accessList.Spec.Owners[1].Description)
}

func TestAuditMarshaling(t *testing.T) {
	audit := Audit{
		NextAuditDate: time.Date(2023, 02, 02, 0, 0, 0, 0, time.UTC),
		Recurrence: Recurrence{
			Frequency:  SixMonths,
			DayOfMonth: LastDayOfMonth,
		},
		Notifications: Notifications{
			Start: 4 * time.Hour,
		},
	}

	data, err := json.Marshal(&audit)
	require.NoError(t, err)

	require.Equal(t, `{"next_audit_date":"2023-02-02T00:00:00Z","recurrence":{"frequency":"6 months","day_of_month":"last"},"notifications":{"start":"4h0m0s"}}`, string(data))
}

func TestAuditUnmarshaling(t *testing.T) {
	raw := map[string]interface{}{
		"next_audit_date": "2023-02-02T00:00:00Z",
		"recurrence": map[string]interface{}{
			"frequency":    "3 months",
			"day_of_month": "1",
		},
		"notifications": map[string]interface{}{
			"start": twoWeeks.String(),
		},
	}

	data, err := json.Marshal(&raw)
	require.NoError(t, err)

	var audit Audit
	require.NoError(t, json.Unmarshal(data, &audit))

	require.Equal(t, time.Date(2023, 02, 02, 0, 0, 0, 0, time.UTC), audit.NextAuditDate)
	require.Equal(t, ThreeMonths, audit.Recurrence.Frequency)
	require.Equal(t, FirstDayOfMonth, audit.Recurrence.DayOfMonth)
	require.Equal(t, twoWeeks, audit.Notifications.Start)
}
