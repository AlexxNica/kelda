const quilt = require('@quilt/quilt');
const infrastructure = require('../../config/infrastructure.js');

const infra = infrastructure.createTestInfrastructure();

const image = 'alpine';
const command = ['tail', '-f', '/dev/null'];

const redContainers = [];
for (let i = 0; i < 5; i += 1) {
  redContainers.push(new quilt.Container('red', image, { command }));
}

const blueContainers = [];
for (let i = 0; i < 5; i += 1) {
  blueContainers.push(new quilt.Container('blue', image, { command }));
}

const yellowContainers = [];
for (let i = 0; i < 5; i += 1) {
  yellowContainers.push(new quilt.Container('yellow', image, { command }));
}

quilt.allow(redContainers, blueContainers, 80);
quilt.allow(blueContainers, redContainers, 80);
quilt.allow(redContainers, yellowContainers, 80);
quilt.allow(blueContainers, yellowContainers, 80);

const redLB = new quilt.LoadBalancer('red-lb', redContainers);
redLB.allowFrom(blueContainers, 80);

redContainers.forEach(container => container.deploy(infra));
yellowContainers.forEach(container => container.deploy(infra));
blueContainers.forEach(container => container.deploy(infra));
redLB.deploy(infra);
