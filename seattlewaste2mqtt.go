package main

import (
	"fmt"
	"log"
	"reflect"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	mqttExtDI "github.com/mannkind/paho.mqtt.golang.ext/di"
	mqttExtHA "github.com/mannkind/paho.mqtt.golang.ext/ha"
	"github.com/mannkind/seattlewaste"
)

const (
	apiDateFormat       = "Mon, 2 Jan 2006"
	sensorTopicTemplate = "%s/%s/state"
	maxAPIAttempts      = 5
)

// SeattleWaste2Mqtt - Lookup collection information on seattle.gov.
type SeattleWaste2Mqtt struct {
	discovery       bool
	discoveryPrefix string
	discoveryName   string
	topicPrefix     string
	address         string
	alertWithin     time.Duration
	lookupInterval  time.Duration

	client mqtt.Client
}

// NewSeattleWaste2Mqtt - Returns a new reference to a fully configured object.
func NewSeattleWaste2Mqtt(config *Config, mqttFuncWrapper *mqttExtDI.MQTTFuncWrapper) *SeattleWaste2Mqtt {
	cl := SeattleWaste2Mqtt{
		discovery:       config.MQTT.Discovery,
		discoveryPrefix: config.MQTT.DiscoveryPrefix,
		discoveryName:   config.MQTT.DiscoveryName,
		topicPrefix:     config.MQTT.TopicPrefix,
		address:         config.Address,
		alertWithin:     config.AlertWithin,
		lookupInterval:  config.LookupInterval,
	}

	opts := mqttFuncWrapper.
		ClientOptsFunc().
		AddBroker(config.MQTT.Broker).
		SetClientID(config.MQTT.ClientID).
		SetOnConnectHandler(cl.onConnect).
		SetConnectionLostHandler(cl.onDisconnect).
		SetUsername(config.MQTT.Username).
		SetPassword(config.MQTT.Password).
		SetWill(cl.availabilityTopic(), "offline", 0, true)

	cl.client = mqttFuncWrapper.ClientFunc(opts)

	return &cl
}

// Run - Start the collection lookup process
func (t *SeattleWaste2Mqtt) Run() error {
	log.Print("Connecting to MQTT")
	if token := t.client.Connect(); !token.Wait() || token.Error() != nil {
		return token.Error()
	}

	t.loop(false)

	return nil
}

func (t *SeattleWaste2Mqtt) onConnect(client mqtt.Client) {
	log.Print("Connected to MQTT")
	t.publish(t.availabilityTopic(), "online")
	t.publishDiscovery()
}

func (t *SeattleWaste2Mqtt) onDisconnect(client mqtt.Client, err error) {
	log.Printf("Disconnected from MQTT: %s.", err)
}

func (t *SeattleWaste2Mqtt) availabilityTopic() string {
	return fmt.Sprintf("%s/status", t.topicPrefix)
}

func (t *SeattleWaste2Mqtt) publishDiscovery() {
	if !t.discovery {
		return
	}

	obj := reflect.ValueOf(seattlewaste.Collection{})
	for i := 0; i < obj.NumField(); i++ {
		sensor := strings.ToLower(obj.Type().Field(i).Name)
		val := obj.Field(i)
		sensorType := ""

		switch val.Kind() {
		case reflect.Bool:
			sensorType = "binary_sensor"
		case reflect.String:
			sensorType = "sensor"
		}

		if sensorType == "" {
			continue
		}

		mqd := mqttExtHA.MQTTDiscovery{
			DiscoveryPrefix: t.discoveryPrefix,
			Component:       sensorType,
			NodeID:          t.discoveryName,
			ObjectID:        sensor,

			AvailabilityTopic: t.availabilityTopic(),
			Name:              fmt.Sprintf("%s %s", t.discoveryName, sensor),
			StateTopic:        fmt.Sprintf(sensorTopicTemplate, t.topicPrefix, sensor),
			UniqueID:          fmt.Sprintf("%s.%s", t.discoveryName, sensor),
		}

		mqd.PublishDiscovery(t.client)
	}
}

func (t *SeattleWaste2Mqtt) loop(once bool) {
	for {
		log.Print("Beginning lookup")
		now := time.Now()
		if collectionInfo, date, err := t.collectionLookup(now); collectionInfo.Start != "" && err == nil {
			t.publishCollectionInfo(collectionInfo, date)
		} else {
			log.Print(err)
		}
		log.Print("Ending lookup")

		if once {
			break
		}

		time.Sleep(t.lookupInterval)
	}
}

func (t *SeattleWaste2Mqtt) collectionLookup(now time.Time) (seattlewaste.Collection, time.Time, error) {
	none := seattlewaste.Collection{}
	swClient := seattlewaste.NewClient(t.address)

	localLoc, _ := time.LoadLocation("Local")
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 1, 0, localLoc)
	firstOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, localLoc)
	lastTimestamp := firstOfMonth.Unix()
	todayTimestamp := today.Unix()
	apiCallCount := 0

	for lastTimestamp < todayTimestamp && apiCallCount <= maxAPIAttempts {
		results, err := swClient.GetCollections(lastTimestamp)
		if err != nil {
			log.Print(err)
			return none, time.Now(), fmt.Errorf("Unable to fetch collection dates")
		}

		apiCallCount++

		if len(results) == 0 {
			return none, time.Now(), fmt.Errorf("No collection dates returned")
		}

		// Results from the 'web-service' do not always return as expected
		for _, result := range results {
			pTime, err := time.ParseInLocation(apiDateFormat, result.Start, localLoc)
			if err != nil {
				log.Print(err)
				continue
			}

			lastTimestamp = pTime.Unix()
			if lastTimestamp >= todayTimestamp {
				return result, pTime, nil
			}
		}
	}

	return none, time.Now(), nil
}

func (t *SeattleWaste2Mqtt) publishCollectionInfo(info seattlewaste.Collection, date time.Time) {
	until := date.Sub(time.Now())
	info.Status = 0 <= until && until <= t.alertWithin

	obj := reflect.ValueOf(info)
	for i := 0; i < obj.NumField(); i++ {
		sensor := strings.ToLower(obj.Type().Field(i).Name)
		val := obj.Field(i)

		topic := fmt.Sprintf(sensorTopicTemplate, t.topicPrefix, sensor)
		payload := ""

		switch val.Kind() {
		case reflect.Bool:
			payload = "OFF"
			if val.Bool() {
				payload = "ON"
			}
		case reflect.String:
			payload = val.String()
		}

		if payload == "" {
			continue
		}

		t.publish(topic, payload)
	}
}

func (t *SeattleWaste2Mqtt) publish(topic string, payload string) {
	retain := true
	if token := t.client.Publish(topic, 0, retain, payload); token.Wait() && token.Error() != nil {
		log.Printf("Publish Error: %s", token.Error())
	}

	log.Print(fmt.Sprintf("Publishing - Topic: %s ; Payload: %s", topic, payload))
}
