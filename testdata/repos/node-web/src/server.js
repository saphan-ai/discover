const http = require("http");
const { userList } = require("./routes/user-list");

function start(port) {
  return http.createServer(userList).listen(port);
}

module.exports = { start };
