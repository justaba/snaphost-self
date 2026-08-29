terraform {
  required_version = ">= 1.7"

  # Bucket and key are supplied per environment with -backend-config.
  # Credentials are supplied through the standard AWS environment variables,
  # never committed backend files.
  backend "s3" {
    endpoints = {
      s3 = "https://storage.yandexcloud.net"
    }
    region                      = "ru-central1"
    skip_region_validation      = true
    skip_credentials_validation = true
    skip_requesting_account_id  = true
    skip_s3_checksum            = true
  }

  required_providers {
    yandex = {
      source  = "yandex-cloud/yandex"
      version = "~> 0.110"
    }
    local = {
      source  = "hashicorp/local"
      version = "~> 2.5"
    }
  }
}

provider "yandex" {
  service_account_key_file = var.bootstrap_key_file
  cloud_id                 = var.cloud_id
  folder_id                = var.folder_id
  zone                     = var.default_zone
}
