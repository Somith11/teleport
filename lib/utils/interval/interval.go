/*
Copyright 2021 Gravitational, Inc.

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

package interval

import (
	"errors"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/gravitational/teleport/api/utils/retryutils"
)

// Interval functions similarly to time.Ticker, with the added benefit of being
// able to specify a custom duration for the first "tick", and an optional
// per-tick jitter.  When attempting to stagger periodic operations it is recommended
// to apply a large jitter to the first duration, and provide a small jitter for the
// per-tick jitter.  This will ensure that operations started at similar times will
// have varying initial interval states, while minimizing the amount of extra work
// introduced by the per-tick jitter.
type Interval struct {
	cfg       Config
	ch        chan time.Time
	reset     chan struct{}
	fire      chan struct{}
	closeOnce sync.Once
	done      chan struct{}
}

// Config configures an interval.  The only required parameter is
// the Duration field which *must* be a positive duration.
type Config struct {
	// Duration is the duration on which the interval "ticks" (if a jitter is
	// applied, this represents the upper bound of the range).
	Duration time.Duration

	// FirstDuration is an optional special duration to be used for the first
	// "tick" of the interval.  This duration is not jittered.
	FirstDuration time.Duration

	// Jitter is an optional jitter to be applied to each step of the interval.
	// It is usually preferable to use a smaller jitter (e.g. NewSeventhJitter())
	// for this parameter, since periodic operations are typically costly and the
	// effect of the jitter is cumulative.
	Jitter retryutils.Jitter

	// Clock is the clock to use to control the interval.
	Clock clockwork.Clock
}

// NewNoop creates a new interval that will never fire.
func NewNoop() *Interval {
	return &Interval{
		ch:   make(chan time.Time, 1),
		done: make(chan struct{}),
	}
}

// New creates a new interval instance.  This function panics on non-positive
// interval durations (equivalent to time.NewTicker).
func New(cfg Config) *Interval {
	if cfg.Duration <= 0 {
		panic(errors.New("non-positive interval for interval.New"))
	}

	clock := cfg.Clock
	if clock == nil {
		clock = clockwork.NewRealClock()
	}

	interval := &Interval{
		ch:    make(chan time.Time, 1),
		cfg:   cfg,
		reset: make(chan struct{}),
		fire:  make(chan struct{}),
		done:  make(chan struct{}),
	}

	firstDuration := cfg.FirstDuration
	if firstDuration == 0 {
		firstDuration = interval.duration()
	}

	// start the timer in this goroutine to improve
	// consistency of first tick.
	timer := clock.NewTimer(firstDuration)

	go interval.run(timer)

	return interval
}

// Stop permanently stops the interval.  Note that stopping an interval does not
// close its output channel.  This is done in order to prevent concurrent stops
// from generating erroneous "ticks" and is consistent with the behavior of
// time.Ticker.
func (i *Interval) Stop() {
	i.closeOnce.Do(func() {
		close(i.done)
	})
}

// Reset resets the interval without pausing it (i.e. it will now fire in
// jitter(duration) regardless of current timer progress).
func (i *Interval) Reset() {
	select {
	case i.reset <- struct{}{}:
	case <-i.done:
	}
}

// FireNow forces the interval to fire immediately regardless of how much time is left on
// the current interval. This also effectively resets the interval.
func (i *Interval) FireNow() {
	select {
	case i.fire <- struct{}{}:
	case <-i.done:
	}
}

// Next is the channel over which the intervals are delivered.
func (i *Interval) Next() <-chan time.Time {
	return i.ch
}

// duration gets the duration of the interval.  Each call applies the jitter
// if one was supplied.
func (i *Interval) duration() time.Duration {
	if i.cfg.Jitter == nil {
		return i.cfg.Duration
	}
	return i.cfg.Jitter(i.cfg.Duration)
}

func (i *Interval) run(timer clockwork.Timer) {
	defer timer.Stop()

	// we take advantage of the fact that sends on nil channels never complete,
	// and only set ch when tick is valid and needs to be sent.
	var tick time.Time
	var ch chan<- time.Time
	for {
		select {
		case tick = <-timer.Chan():
			// timer has fired, reset to next duration and ensure that
			// output channel is set.
			timer.Reset(i.duration())
			ch = i.ch
		case <-i.reset:
			// stop and drain timer
			if !timer.Stop() {
				<-timer.Chan()
			}
			// re-set the timer
			timer.Reset(i.duration())
			// ensure we don't send any pending ticks
			ch = nil
		case <-i.fire:
			// stop and drain timer
			if !timer.Stop() {
				<-timer.Chan()
			}
			// re-set the timer
			timer.Reset(i.duration())
			// simulate firing of the timer
			tick = time.Now()
			ch = i.ch
		case ch <- tick:
			// tick has been sent, set ch back to nil to prevent
			// double-send and wait for next timer firing
			ch = nil
		case <-i.done:
			// interval has been stopped.
			return
		}
	}
}
