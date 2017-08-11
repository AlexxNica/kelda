const quilt = require('@quilt/quilt');
let spark = require('@quilt/spark');
let infrastructure = require('../../config/infrastructure.js');

let deployment = quilt.createDeployment();
deployment.deploy(infrastructure);

let sprk = new spark.Spark(1, 3);

deployment.deploy(sprk);
