const quilt = require('@quilt/quilt');
const etcd = require('@quilt/etcd');
const infrastructure = require('../../config/infrastructure.js');

const deployment = new quilt.Deployment();
deployment.deploy(infrastructure);
const etcdApp = new etcd.Etcd(infrastructure.nWorker * 2);
etcdApp.deploy(deployment);
