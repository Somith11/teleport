/*
Copyright 2020 Gravitational, Inc.

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

const path = require('path');

module.exports = {
  testEnvironment: path.join(__dirname, 'jest-environment-patched-jsdom.js'),
  moduleNameMapper: {
    // mock all imports to asset files
    '\\.(css|scss|stylesheet)$': path.join(__dirname, 'mockStyles.js'),
    '\\.(png|svg|yaml|yaml\\?raw)$': path.join(__dirname, 'mockFiles.js'),
    '^shared/(.*)$': '<rootDir>/web/packages/shared/$1',
    '^design($|/.*)': '<rootDir>/web/packages/design/src/$1',
    '^teleport($|/.*)': '<rootDir>/web/packages/teleport/src/$1',
    '^teleterm($|/.*)': '<rootDir>/web/packages/teleterm/src/$1',
    '^e-teleport/(.*)$': '<rootDir>/e/web/teleport/src/$1',
    '^e-teleterm/(.*)$': '<rootDir>/e/web/teleterm/src/$1',
    '^gen-proto-js/(.*)$': '<rootDir>/gen/proto/js/$1',
  },
  // Keep pre-v29 snapshot format to avoid existing snapshots breaking.
  // https://jestjs.io/docs/upgrading-to-jest29#snapshot-format
  snapshotFormat: {
    escapeString: true,
    printBasicPrototype: true,
  },
};
