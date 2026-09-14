package schedule

import (
	"errors"
	"fmt"
)

// Validate checks the club's rule directly: every car goes down the track once
// in each lane, and no car appears twice in a single heat.
//
// This is cheap, so it runs before a schedule is written to the database. A
// scheduling bug that reached race night would be discovered by a racer being
// called to two lanes at once, which is not how anyone wants to find out.
func Validate(s *Schedule) error {
	if s == nil {
		return errors.New("schedule: nil")
	}
	if len(s.Heats) == 0 {
		return errors.New("schedule: no heats")
	}

	// perLane[car][lane] counts appearances.
	perLane := make([][]int, s.Cars)
	for i := range perLane {
		perLane[i] = make([]int, s.Lanes)
	}

	for h, heat := range s.Heats {
		if len(heat) != s.Lanes {
			return fmt.Errorf("schedule: heat %d has %d lanes, want %d", h+1, len(heat), s.Lanes)
		}
		seen := make(map[int]bool, s.Lanes)
		for lane, car := range heat {
			if car == Bye {
				continue
			}
			if car < 0 || car >= s.Cars {
				return fmt.Errorf("schedule: heat %d lane %d has car index %d, out of range", h+1, lane+1, car)
			}
			if seen[car] {
				return fmt.Errorf("schedule: car %d appears twice in heat %d", car, h+1)
			}
			seen[car] = true
			perLane[car][lane]++
		}
	}

	for car := 0; car < s.Cars; car++ {
		total := 0
		for lane := 0; lane < s.Lanes; lane++ {
			n := perLane[car][lane]
			if n != 1 {
				return fmt.Errorf("schedule: car %d runs lane %d %d time(s), want exactly 1", car, lane+1, n)
			}
			total += n
		}
		if total != s.Lanes {
			return fmt.Errorf("schedule: car %d has %d runs, want %d", car, total, s.Lanes)
		}
	}
	return nil
}

// Meetings counts how many times each unordered pair of cars shares a heat.
// It is the direct, obvious computation, used to check the analytic score in
// scoreOffsets is telling the truth.
func Meetings(s *Schedule) map[[2]int]int {
	out := make(map[[2]int]int)
	for _, heat := range s.Heats {
		for i := 0; i < len(heat); i++ {
			for j := i + 1; j < len(heat); j++ {
				a, b := heat[i], heat[j]
				if a == Bye || b == Bye {
					continue
				}
				if a > b {
					a, b = b, a
				}
				out[[2]int{a, b}]++
			}
		}
	}
	return out
}

// RepeatCount sums how often pairs meet beyond the first time.
func RepeatCount(s *Schedule) int {
	total := 0
	for _, n := range Meetings(s) {
		if n > 1 {
			total += n - 1
		}
	}
	return total
}

// MinGap reports the smallest number of heats between any car's consecutive
// runs. 1 means some car raced in back-to-back heats.
func MinGap(s *Schedule) int {
	last := make([]int, s.Cars)
	for i := range last {
		last[i] = -1
	}
	min := len(s.Heats) + 1
	for h, heat := range s.Heats {
		for _, car := range heat {
			if car == Bye {
				continue
			}
			if last[car] >= 0 {
				if gap := h - last[car]; gap < min {
					min = gap
				}
			}
			last[car] = h
		}
	}
	return min
}
