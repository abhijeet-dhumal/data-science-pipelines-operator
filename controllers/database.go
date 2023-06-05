/*

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"database/sql"
	b64 "encoding/base64"
	"fmt"

	_ "github.com/go-sql-driver/mysql"

	dspav1alpha1 "github.com/opendatahub-io/data-science-pipelines-operator/api/v1alpha1"
)

const dbSecret = "mariadb/secret.yaml.tmpl"

var dbTemplates = []string{
	"mariadb/deployment.yaml.tmpl",
	"mariadb/pvc.yaml.tmpl",
	"mariadb/sa.yaml.tmpl",
	"mariadb/service.yaml.tmpl",
	dbSecret,
}

func (r *DSPAReconciler) VerifyMySQLDBConnection(host, port, username, password, dbname string) bool {
	connectionString := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s", username, password, host, port, dbname)
	db, err := sql.Open("mysql", connectionString)
	if err != nil {
		return false
	}
	defer db.Close()

	testStatement := "SELECT 1;"
	_, err = db.Exec(testStatement)
	return err == nil
}

func (r *DSPAReconciler) isDatabaseAccessible(ctx context.Context, dsp *dspav1alpha1.DataSciencePipelinesApplication,
	params *DSPAParams) bool {
	log := r.Log.WithValues("namespace", dsp.Namespace).WithValues("dspa_name", dsp.Name)

	log.Info("Performing Database Health Check")
	databaseSpecified := dsp.Spec.Database != nil
	usingExternalDB := params.UsingExternalDB(dsp)
	usingMariaDB := !databaseSpecified || dsp.Spec.Database.MariaDB != nil
	if usingMariaDB || usingExternalDB {
		decodePass, _ := b64.StdEncoding.DecodeString(params.DBConnection.Password)
		db_connect := r.VerifyMySQLDBConnection(params.DBConnection.Host,
			params.DBConnection.Port,
			params.DBConnection.Username,
			string(decodePass),
			params.DBConnection.DBName)
		if db_connect {
			log.Info("Database Health Check Successful")
		} else {
			log.Info("Unable to connect to Database")
		}
		return db_connect

	}

	log.Info(fmt.Sprintf("Could not connect to Database: Unsupported Type"))
	// Only MariaDB and Mysql-Compliant Database supported.
	return false
}

func (r *DSPAReconciler) ReconcileDatabase(ctx context.Context, dsp *dspav1alpha1.DataSciencePipelinesApplication,
	params *DSPAParams) error {

	log := r.Log.WithValues("namespace", dsp.Namespace).WithValues("dspa_name", dsp.Name)

	databaseSpecified := dsp.Spec.Database != nil
	// DB field can be specified as an empty obj, confirm that subfields are also specified
	// By default if Database is empty, we deploy mariadb
	externalDBSpecified := params.UsingExternalDB(dsp)
	mariaDBSpecified := !databaseSpecified || dsp.Spec.Database.MariaDB != nil
	deployMariaDB := !databaseSpecified || (mariaDBSpecified && dsp.Spec.Database.MariaDB.Deploy)

	// If external db is specified, it takes precedence
	if externalDBSpecified {
		log.Info("Deploying external db secret.")
		// If using external DB, we just need to create the secret
		// for apiserver
		err := r.Apply(dsp, params, dbSecret)
		if err != nil {
			return err
		}
	} else if deployMariaDB {
		log.Info("Applying mariaDB resources.")
		for _, template := range dbTemplates {
			err := r.Apply(dsp, params, template)
			if err != nil {
				return err
			}
		}
		// If no database was not specified, deploy mariaDB by default.
		// Update the CR with the state of mariaDB to accurately portray
		// desired state.
		if !databaseSpecified {
			dsp.Spec.Database = &dspav1alpha1.Database{}
			dsp.Spec.Database.MariaDB = params.MariaDB.DeepCopy()
			dsp.Spec.Database.MariaDB.Deploy = true
			if err := r.Update(ctx, dsp); err != nil {
				return err
			}
		}
	} else {
		log.Info("No externalDB detected, and mariaDB disabled. " +
			"skipping Application of DB Resources")
		return nil
	}
	log.Info("Finished applying Database Resources")

	return nil
}
