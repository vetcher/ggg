package innerdir

import (
	"ggg/test"
)

//ggg:convert
func fff(in test.Y) X { panic("todo") }

//ggg:convert
func fff1(in *test.Y) X {
	panic("todo")
}

//ggg:convert
func fff2(in test.Y) *X {
	panic("todo")
}

//ggg:convert
func fff3(in *test.Y) *X {
	return &X{}
}
