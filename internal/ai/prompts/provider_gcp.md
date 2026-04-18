---
id: provider_gcp
description: Resource-type guide for the GCP (google) provider.
---
Google Cloud (GCP) resource types ONLY. Examples:
- Networking: google_compute_network, google_compute_subnetwork, google_compute_firewall, google_compute_router
- Compute: google_compute_instance, google_container_cluster, google_cloud_run_service, google_cloudfunctions_function
- Storage: google_storage_bucket, google_compute_disk
- Database: google_sql_database_instance, google_redis_instance, google_spanner_instance, google_firestore_database
- Security: google_service_account, google_kms_key_ring, google_secret_manager_secret
- Messaging: google_pubsub_topic, google_pubsub_subscription
- Data: google_bigquery_dataset, google_bigquery_table
NEVER use aws_ or azurerm_ prefixed resources
