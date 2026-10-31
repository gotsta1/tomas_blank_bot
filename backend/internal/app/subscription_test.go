package app

import (
	"testing"
	"time"

	"github.com/gotsta1/tomas_blank_bot/backend/internal/lava"
)

func TestSubscriptionExpiryAcceptsLavaPeriodicityFormats(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, time.September, 21, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		periodicity string
		want        time.Time
	}{
		{periodicity: "monthly", want: started.AddDate(0, 1, 0)},
		{periodicity: "period_90", want: started.Add(90 * 24 * time.Hour)},
		{periodicity: "PERIOD_90_DAYS", want: started.Add(90 * 24 * time.Hour)},
		{periodicity: "period_180", want: started.Add(180 * 24 * time.Hour)},
		{periodicity: "PERIOD_180_DAYS", want: started.Add(180 * 24 * time.Hour)},
		{periodicity: "period_year", want: started.AddDate(1, 0, 0)},
	}
	for _, tc := range tests {
		t.Run(tc.periodicity, func(t *testing.T) {
			expires, err := subscriptionExpiry(lava.Subscription{
				Status:             "COMPLETED",
				SubscriptionStatus: "ACTIVE",
				Periodicity:        tc.periodicity,
				DateTime:           started,
				Receipt:            lava.Receipt{Amount: "0", Currency: "RUB"},
			})
			if err != nil || !expires.Equal(tc.want) {
				t.Fatalf("expires=%v want=%v err=%v", expires, tc.want, err)
			}
		})
	}
}
