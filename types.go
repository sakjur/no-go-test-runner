package main

import (
	"encoding/json"
	"time"
)

type Elapsed time.Duration

func (e Elapsed) String() string {
	return time.Duration(e).String()
}

func (e *Elapsed) MarshalJSON() ([]byte, error) {
	if e == nil {
		return nil, nil
	}
	return []byte(`"` + e.String() + `"`), nil
}

func (e *Elapsed) UnmarshalJSON(bytes []byte) error {
	var s float64
	err := json.Unmarshal(bytes, &s)
	if err != nil {
		return err
	}
	*e = Elapsed(s * float64(time.Second))
	return nil
}

type GoTestLine struct {
	Time    time.Time
	Action  string
	Package string
	Elapsed Elapsed
	Output  string
	Test    string
}
