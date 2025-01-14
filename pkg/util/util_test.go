package util

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestErrList_VerifyErrorConcatenation(t *testing.T) {
	errList := []error{
		fmt.Errorf("error1"),
		fmt.Errorf("error2"),
		fmt.Errorf("error3"),
		fmt.Errorf("error4"),
		fmt.Errorf("error5"),
	}

	mergedErrors := ErrList(errList)
	assert.ErrorContains(t, mergedErrors, "error1")
	assert.ErrorContains(t, mergedErrors, "error5")

	errList = []error{
		fmt.Errorf("error1"),
	}

	mergedErrors = ErrList(errList)
	assert.ErrorContains(t, mergedErrors, "error1")
}

func TestErrList_VerifyNilOnEmptyList(t *testing.T) {
	errList := []error{}

	mergedErrors := ErrList(errList)
	assert.Nil(t, mergedErrors)
}
