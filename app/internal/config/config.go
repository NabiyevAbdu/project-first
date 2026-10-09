package config

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/ilyakaznacheev/cleanenv"
)

type Config struct {
	BotToken string `env:"BOT_TOKEN" env-required:"true"`
	GroupURL string `env:"GROUP_URL"`
}

var instance *Config
var once sync.Once

func GetConfig() *Config {
	once.Do(func() {
		log.Print("gather config")

		instance = &Config{}

		rootPath := flag.String("root_path", "../", "Root path")
		flag.Parse()

		envFilePath, err := filepath.Abs(*rootPath + ".env")
		if err != nil {
			fmt.Println("Env file path error: ", err)
		}

		// A local .env file is optional. Hosted services provide configuration
		// directly through environment variables instead.
		if _, err := os.Stat(envFilePath); err == nil {
			if err := cleanenv.ReadConfig(envFilePath, instance); err != nil {
				log.Printf("failed to read config file: %v", err)
			}
		} else if !os.IsNotExist(err) {
			log.Printf("failed to access config file: %v", err)
		}

		if err := cleanenv.ReadEnv(instance); err != nil {
			helpText := "Saidoff - Reading project!"
			help, _ := cleanenv.GetDescription(instance, &helpText)
			log.Print(help)
			log.Printf("failed to read environment config: %v", err)
		}
	})
	return instance
}
