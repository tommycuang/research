# Keyset pagination avoids deep offset work

The learner predicted and verified that an ordered composite index still walks and discards 1,500 entries for deep offset pagination, while a tuple cursor starts at the next position and returns 50 rows with 54 buffers instead of 1,564. Future sessions can build on this distinction when discussing stable cursors and pagination under growing offsets.
