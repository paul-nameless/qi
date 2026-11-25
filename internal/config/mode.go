package config

type Mode int32

const (
	ModeYolo Mode = iota
	ModeProject
	ModeReadOnly
)

var modeNames = []string{ModeYolo: "yolo", ModeProject: "project", ModeReadOnly: "read-only"}

func (m Mode) String() string {
	if m < 0 || int(m) >= len(modeNames) {
		return modeNames[ModeYolo]
	}
	return modeNames[m]
}

func (m Mode) Next() Mode {
	switch m {
	case ModeYolo:
		return ModeReadOnly
	case ModeReadOnly:
		return ModeProject
	}
	return ModeYolo
}

func ParseMode(s string) Mode {
	for m, name := range modeNames {
		if name == s {
			return Mode(m)
		}
	}
	return ModeYolo
}
