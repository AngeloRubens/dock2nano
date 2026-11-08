-- The tables that the application creates when it is deployed (taken from a plain docker compose run).
-- On nanos the application is only loaded at boot, not deployed, so the package of the database
-- must already have them.
SET FOREIGN_KEY_CHECKS=0;
CREATE TABLE `owner` (
  `ID` bigint NOT NULL AUTO_INCREMENT,
  `address` varchar(255) NOT NULL,
  `address_info` varchar(255) DEFAULT NULL,
  `city` varchar(255) NOT NULL,
  `email` varchar(255) NOT NULL,
  `first_name` varchar(255) NOT NULL,
  `housenumber` varchar(255) NOT NULL,
  `lastName` varchar(255) NOT NULL,
  `phonenumber` varchar(255) NOT NULL,
  `searchindex` varchar(255) DEFAULT NULL,
  `uuid` varchar(36) NOT NULL,
  `zipcode` varchar(255) NOT NULL,
  PRIMARY KEY (`ID`),
  UNIQUE KEY `email` (`email`),
  UNIQUE KEY `uuid` (`uuid`),
  UNIQUE KEY `owner_unique_email` (`email`),
  UNIQUE KEY `owner_unique_uuid` (`uuid`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE `owner_pet` (
  `ID` bigint NOT NULL AUTO_INCREMENT,
  `birth_date` date DEFAULT NULL,
  `name` varchar(255) NOT NULL,
  `searchindex` varchar(255) DEFAULT NULL,
  `uuid` varchar(36) NOT NULL,
  `owner_id` bigint DEFAULT NULL,
  `owner_pet_pettype_id` bigint DEFAULT NULL,
  PRIMARY KEY (`ID`),
  UNIQUE KEY `uuid` (`uuid`),
  UNIQUE KEY `owner_pet_unique_uuid` (`uuid`),
  UNIQUE KEY `owner_pet_unique_names` (`name`,`birth_date`,`owner_pet_pettype_id`,`owner_id`),
  KEY `FK_owner_pet_owner_pet_pettype_id` (`owner_pet_pettype_id`),
  KEY `FK_owner_pet_owner_id` (`owner_id`),
  CONSTRAINT `FK_owner_pet_owner_id` FOREIGN KEY (`owner_id`) REFERENCES `owner` (`ID`),
  CONSTRAINT `FK_owner_pet_owner_pet_pettype_id` FOREIGN KEY (`owner_pet_pettype_id`) REFERENCES `owner_pet_pettype` (`ID`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE `owner_pet_pettype` (
  `ID` bigint NOT NULL AUTO_INCREMENT,
  `name` varchar(255) NOT NULL,
  `searchindex` varchar(255) DEFAULT NULL,
  `uuid` varchar(36) NOT NULL,
  PRIMARY KEY (`ID`),
  UNIQUE KEY `name` (`name`),
  UNIQUE KEY `uuid` (`uuid`),
  UNIQUE KEY `owner_pet_pettype_unique_names` (`name`),
  UNIQUE KEY `owner_pet_pettype_unique_uuid` (`uuid`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE `owner_pet_visit` (
  `ID` bigint NOT NULL AUTO_INCREMENT,
  `visit_date` date NOT NULL,
  `description` varchar(255) NOT NULL,
  `searchindex` varchar(255) DEFAULT NULL,
  `uuid` varchar(36) NOT NULL,
  `owner_pet_id` bigint DEFAULT NULL,
  PRIMARY KEY (`ID`),
  UNIQUE KEY `uuid` (`uuid`),
  UNIQUE KEY `owner_pet_visit_unique_uuid` (`uuid`),
  UNIQUE KEY `owner_pet_visit_unique_names` (`visit_date`,`description`,`owner_pet_id`),
  KEY `FK_owner_pet_visit_owner_pet_id` (`owner_pet_id`),
  CONSTRAINT `FK_owner_pet_visit_owner_pet_id` FOREIGN KEY (`owner_pet_id`) REFERENCES `owner_pet` (`ID`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE `specialty` (
  `ID` bigint NOT NULL AUTO_INCREMENT,
  `name` varchar(255) NOT NULL,
  `searchindex` varchar(255) DEFAULT NULL,
  `uuid` varchar(36) NOT NULL,
  PRIMARY KEY (`ID`),
  UNIQUE KEY `name` (`name`),
  UNIQUE KEY `uuid` (`uuid`),
  UNIQUE KEY `specialty_unique_names` (`name`),
  UNIQUE KEY `specialty_unique_uuid` (`uuid`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE `vet` (
  `ID` bigint NOT NULL AUTO_INCREMENT,
  `first_name` varchar(255) NOT NULL,
  `lastName` varchar(255) NOT NULL,
  `searchindex` varchar(255) DEFAULT NULL,
  `uuid` varchar(36) NOT NULL,
  PRIMARY KEY (`ID`),
  UNIQUE KEY `uuid` (`uuid`),
  UNIQUE KEY `vet_unique_uuid` (`uuid`),
  UNIQUE KEY `vet_unique_names` (`first_name`,`lastName`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE `vet_specialties` (
  `vet_id` bigint NOT NULL,
  `specialty_id` bigint NOT NULL,
  PRIMARY KEY (`vet_id`,`specialty_id`),
  KEY `FK_vet_specialties_specialty_id` (`specialty_id`),
  CONSTRAINT `FK_vet_specialties_specialty_id` FOREIGN KEY (`specialty_id`) REFERENCES `specialty` (`ID`),
  CONSTRAINT `FK_vet_specialties_vet_id` FOREIGN KEY (`vet_id`) REFERENCES `vet` (`ID`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
