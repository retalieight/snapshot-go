package pushover

import (
	"fmt"
	"github.com/gregdel/pushover"
	"log"
	"os"
)

func PushNotify(enabled bool, apiKey, userKey, msgTitle, msgText string) {
	if enabled && apiKey != "" && userKey != "" {
		hostname, err := os.Hostname()
		if err != nil {
			log.Fatal("Unable to determine hostname")
		}
		messageTitle := fmt.Sprintf("%s - %s", hostname, msgTitle)
		pushOver := pushover.New(apiKey)
		pushRecipient := pushover.NewRecipient(userKey)
		pushMessage := &pushover.Message{
			Title:   messageTitle,
			Message: msgText,
		}
		pushResponse, pushErr := pushOver.SendMessage(pushMessage, pushRecipient)
		if pushErr != nil {
			log.Panic(pushErr)
		}
		log.Println(pushResponse)
	} else {
		fmt.Println("Pushover notifications are not enabled.")
	}
}
