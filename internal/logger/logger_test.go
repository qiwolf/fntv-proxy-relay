package logger

import (
	"sync"
	"testing"
)

func TestConcurrentLevelReload(t *testing.T) {
	l := New("error", "")
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 100; n++ {
				l.SetLevel("warn")
				l.GetLevel()
				l.Debug("suppressed")
			}
		}()
	}
	wg.Wait()
}
