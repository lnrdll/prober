package output

var (
	BindStringFlag func(name, value, usage string, target *string)
	BindBoolFlag   func(name string, value bool, usage string, target *bool)
)

func LinkFlagBinders(strBinder func(name, value, usage string, target *string), boolBinder func(name string, value bool, usage string, target *bool)) {
	BindStringFlag = strBinder
	BindBoolFlag = boolBinder
}
