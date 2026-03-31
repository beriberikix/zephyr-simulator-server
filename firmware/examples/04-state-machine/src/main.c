#include <zephyr/kernel.h>
#include <stdio.h>

enum state { INIT, READY, RUNNING, CLEANUP, DONE };

int main(void)
{
	setvbuf(stdout, NULL, _IONBF, 0);
	setvbuf(stderr, NULL, _IONBF, 0);

	enum state current = INIT;
	const char *state_names[] = {"INIT", "READY", "RUNNING", "CLEANUP", "DONE"};
	
	while (current != DONE) {
		printf("State: %s\n", state_names[current]);
		current = (current + 1) % (DONE + 1);
		k_msleep(500);
	}
	return 0;
}
