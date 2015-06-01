// Copyright 2015 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ratelimit

import (
	"time"

	"golang.org/x/net/context"
)

// A simple interface for limiting the rate of some event. Unlike TokenBucket,
// does not allow the user control over what time means.
type Throttle interface {
	// Return the maximum number of tokens that can be requested in a call to
	// Wait.
	Capacity() (c uint64)

	// Block until the supplied number of tokens can be secured from the
	// underlying token bucket, returning early with false if the context is
	// cancelled before then.
	//
	// REQUIRES: tokens <= capacity
	Wait(ctx context.Context, tokens uint64) (ok bool)
}

// Create a throttle that uses time.Now to judge the time given to the
// underlying token bucket.
//
// Be aware of the monotonicity issues. In particular:
//
//  *  If the system clock jumps into the future, the throttle will let through
//     a burst of traffic.
//
//  *  If the system clock jumps into the past, it will halt all traffic for
//     a potentially very long amount of time.
//
func NewThrottle(
	rateHz float64,
	capacity uint64) (t Throttle) {
	t = &throttle{
		bucket:    NewTokenBucket(rateHz, capacity),
		startTime: time.Now(),
	}

	return
}

type throttle struct {
	bucket    TokenBucket
	startTime time.Time
}

func (t *throttle) Capacity() (c uint64) {
	c = t.bucket.Capacity()
	return
}

func (t *throttle) Wait(
	ctx context.Context,
	tokens uint64) (ok bool) {
	panic("TODO: Wait")
}
