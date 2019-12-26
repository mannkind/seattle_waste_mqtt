// Code generated by Wire. DO NOT EDIT.

//go:generate wire
//+build !wireinject

package main

import (
	"github.com/mannkind/twomqtt"
)

// Injectors from wire.go:

func initialize() *app {
	mainOpts := newOpts()
	mainSourceOpts := mainOpts.Source
	mainComms := newComms()
	v := mainComms.output
	mainSource := newSource(mainSourceOpts, v)
	mainSinkOpts := mainOpts.Sink
	mqttOpts := mainSinkOpts.MQTTOpts
	mqtt := twomqtt.NewMQTT(mqttOpts)
	v2 := mainComms.input
	mainSink := newSink(mqtt, mainSinkOpts, v2)
	mainApp := newApp(mainSource, mainSink)
	return mainApp
}
