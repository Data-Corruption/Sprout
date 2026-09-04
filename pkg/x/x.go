package x

import (
	"fmt"
	"math/rand/v2"
	"time"
)

// Fine, I’ll do it myself...
func Ternary[T any](condition bool, trueValue, falseValue T) T {
	if condition {
		return trueValue
	}
	return falseValue
}

// Typewrite prints text with a realistic typewriter effect.
// speed is approximate characters/sec.
func Typewrite(text string, speed float64) {
	if speed <= 0 {
		speed = 20
	}
	baseDelay := time.Second / time.Duration(speed)
	for _, r := range text {
		fmt.Printf("%c", r)
		// jitter: skewed to feel human.
		// range: 0.3x to 2.0x the base delay.
		jitter := 0.3 + rand.Float64()*1.7
		// extra pauses for punctuation.
		switch r {
		case '.', ',', ';', ':':
			jitter *= 2.0
		case '!', '?':
			jitter *= 2.5
		}
		time.Sleep(time.Duration(float64(baseDelay) * jitter))
	}
}
